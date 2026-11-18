package analysis

import (
	"bytes"
	"strings"
	"testing"

	"activity-descriptor/internal/domain"
)

func insightPtr(value string) *string { return &value }

func insightEvidenceRecord(id, sourceID, quote string) domain.Evidence {
	return domain.Evidence{ID: id, SourceID: sourceID, Quote: quote}
}

func insightNode(id string, nodeType domain.NodeType, title string, parentID *string, evidence ...domain.Evidence) domain.Node {
	return domain.Node{ID: id, Type: nodeType, Title: title, ParentID: parentID, Evidence: evidence}
}

func insightSource(id string, sourceType domain.SourceType, title string) domain.Source {
	return domain.Source{ID: id, Type: sourceType, Title: title}
}

func insightCodesByNode(flags []Flag) map[string][]string {
	out := make(map[string][]string, len(flags))
	for _, flag := range flags {
		out[flag.NodeID] = flag.Codes
	}
	return out
}

func insightContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// TestBuildInsightsProvenance covers the source/source-type/evidence counts,
// evidence deduplication and the unknown-source fallback.
func TestBuildInsightsProvenance(t *testing.T) {
	interviewSource := insightSource("11111111-1111-1111-1111-111111111111", domain.SourceInterview, "Интервью")
	standardSource := insightSource("22222222-2222-2222-2222-222222222222", domain.SourceStandard, "Профстандарт")
	root := insightPtr("n1")

	nodes := []domain.Node{
		insightNode("n1", domain.NodeRole, "Аналитик данных", nil,
			insightEvidenceRecord("e1", interviewSource.ID, "цитата 1"),
			insightEvidenceRecord("e2", interviewSource.ID, "цитата 2"),
			insightEvidenceRecord("e1", interviewSource.ID, "цитата 1"), // duplicate id
		),
		insightNode("n2", domain.NodeFunction, "Отчётность", root,
			insightEvidenceRecord("e3", standardSource.ID, "цитата 3"),
			insightEvidenceRecord("e4", "99999999-9999-9999-9999-999999999999", "цитата 4"),
		),
		insightNode("n3", domain.NodeTask, "Писать SQL-запросы", insightPtr("n2")),
	}

	insights := BuildInsights(nodes, []domain.Source{interviewSource, standardSource}, nil)

	if len(insights.Provenance.Nodes) != 3 {
		t.Fatalf("provenance nodes = %d, want 3 (every node must be present)", len(insights.Provenance.Nodes))
	}
	rootProvenance := insights.Provenance.Nodes["n1"]
	if rootProvenance.EvidenceCount != 2 || rootProvenance.SourceCount != 1 {
		t.Fatalf("root provenance = %+v, want evidence_count 2, source_count 1", rootProvenance)
	}
	if len(rootProvenance.SourceTypes) != 1 || rootProvenance.SourceTypes[0] != "interview" {
		t.Fatalf("root source types = %v, want [interview]", rootProvenance.SourceTypes)
	}
	functionProvenance := insights.Provenance.Nodes["n2"]
	if functionProvenance.EvidenceCount != 2 || functionProvenance.SourceCount != 2 {
		t.Fatalf("function provenance = %+v, want evidence_count 2, source_count 2", functionProvenance)
	}
	if want := []string{"standard", "document"}; len(functionProvenance.SourceTypes) != 2 ||
		functionProvenance.SourceTypes[0] != want[0] || functionProvenance.SourceTypes[1] != want[1] {
		t.Fatalf("function source types = %v, want %v (unknown id -> document)", functionProvenance.SourceTypes, want)
	}
	taskProvenance := insights.Provenance.Nodes["n3"]
	if taskProvenance.EvidenceCount != 0 || taskProvenance.SourceCount != 0 || len(taskProvenance.SourceTypes) != 0 {
		t.Fatalf("empty node provenance = %+v, want zeroes with empty source_types", taskProvenance)
	}
	if taskProvenance.SourceTypes == nil {
		t.Fatal("source_types must be an empty slice, not nil")
	}
}

// TestBuildInsightsHealthScore checks the documented formula on a tree with
// one ungrounded childless task: 4/5 grounded, 4/5 complete, no duplicates,
// no orphans -> 0.4*80 + 0.3*80 + 0.2*100 + 0.1*100 = 86.
func TestBuildInsightsHealthScore(t *testing.T) {
	source := insightSource("11111111-1111-1111-1111-111111111111", domain.SourceInterview, "Интервью")
	evidence := func(id string) domain.Evidence {
		return insightEvidenceRecord(id, source.ID, "цитата "+id)
	}
	rootID, functionID, task1ID := "n1", "n2", "n3"

	nodes := []domain.Node{
		insightNode(rootID, domain.NodeRole, "Аналитик данных", nil, evidence("e1")),
		insightNode(functionID, domain.NodeFunction, "Отчётность", insightPtr(rootID), evidence("e2")),
		insightNode(task1ID, domain.NodeTask, "Писать SQL-запросы", insightPtr(functionID), evidence("e3")),
		insightNode("n4", domain.NodeTask, "Проверять качество данных", insightPtr(functionID)),
		insightNode("n5", domain.NodeAction, "Сверять метрики", insightPtr(task1ID), evidence("e4")),
	}

	health := BuildInsights(nodes, []domain.Source{source}, nil).Health
	wantBreakdown := HealthBreakdown{Groundedness: 80, Completeness: 80, Duplicates: 100, Orphans: 100}
	if health.Breakdown != wantBreakdown {
		t.Fatalf("breakdown = %+v, want %+v", health.Breakdown, wantBreakdown)
	}
	if health.Score != 86 {
		t.Fatalf("score = %d, want 86", health.Score)
	}
	wantCounts := HealthCounts{NodeCount: 5, GroundedNodes: 4, DuplicateNodes: 0, OrphanNodes: 0, ChildlessNodes: 1}
	if health.Counts != wantCounts {
		t.Fatalf("counts = %+v, want %+v", health.Counts, wantCounts)
	}
	if health.Formula != InsightsHealthFormula {
		t.Fatalf("formula = %q, want the documented constant", health.Formula)
	}
	if health.Weights["groundedness"] != 0.40 || health.Weights["orphans"] != 0.10 {
		t.Fatalf("weights = %v, want documented weights", health.Weights)
	}
}

// TestBuildInsightsHealthEmptyTree pins the empty-tree contract: a zero score
// and no panic on nil/empty collections.
func TestBuildInsightsHealthEmptyTree(t *testing.T) {
	source := insightSource("11111111-1111-1111-1111-111111111111", domain.SourceStandard, "Стандарт")
	insights := BuildInsights(nil, []domain.Source{source}, nil)

	if insights.Health.Score != 0 || insights.Health.Breakdown != (HealthBreakdown{}) {
		t.Fatalf("empty tree health = %+v, want zero score and breakdown", insights.Health)
	}
	if len(insights.Provenance.Nodes) != 0 || insights.Provenance.Nodes == nil {
		t.Fatalf("empty tree provenance = %#v, want non-nil empty map", insights.Provenance.Nodes)
	}
	if insights.Flags == nil || len(insights.Flags) != 0 {
		t.Fatalf("empty tree flags = %#v, want non-nil empty slice", insights.Flags)
	}
	if len(insights.Coverage.Columns) != 4 {
		t.Fatalf("coverage columns = %v, want 4 fixed columns", insights.Coverage.Columns)
	}
	if insights.Coverage.Rows == nil || len(insights.Coverage.Rows) != 0 {
		t.Fatalf("empty tree coverage rows = %#v, want non-nil empty slice", insights.Coverage.Rows)
	}
	if insights.MarketHeat.Source != "none" || len(insights.MarketHeat.Top) != 0 || insights.MarketHeat.Nodes == nil {
		t.Fatalf("empty tree market heat = %+v, want none/empty", insights.MarketHeat)
	}
	if insights.NodeExplanations == nil || len(insights.NodeExplanations) != 0 {
		t.Fatalf("empty tree explanations = %#v, want non-nil empty map", insights.NodeExplanations)
	}
	if len(insights.Sources) != 1 || insights.Sources[0].ID != source.ID {
		t.Fatalf("sources = %+v, want the run source preserved", insights.Sources)
	}
}

// TestBuildInsightsHealthDuplicatesAndOrphans verifies the duplicate and
// orphan components: 2 duplicated tasks (uniqueness 50) and 1 orphan task
// (integrity 75), score = round(40 + 15 + 10 + 7.5) = 73.
func TestBuildInsightsHealthDuplicatesAndOrphans(t *testing.T) {
	source := insightSource("11111111-1111-1111-1111-111111111111", domain.SourceInterview, "Интервью")
	evidence := func(id string) domain.Evidence {
		return insightEvidenceRecord(id, source.ID, "цитата "+id)
	}
	rootID, functionID := "n1", "n2"

	nodes := []domain.Node{
		insightNode(rootID, domain.NodeRole, "Аналитик данных", nil, evidence("e1")),
		insightNode(functionID, domain.NodeFunction, "Отчётность", insightPtr(rootID), evidence("e2")),
		insightNode("n3", domain.NodeTask, "Писать SQL-запросы", insightPtr(functionID), evidence("e3")),
		insightNode("n4", domain.NodeTask, "Писать SQL-запросы", insightPtr("missing-parent"), evidence("e4")),
	}

	health := BuildInsights(nodes, []domain.Source{source}, nil).Health
	wantBreakdown := HealthBreakdown{Groundedness: 100, Completeness: 50, Duplicates: 50, Orphans: 75}
	if health.Breakdown != wantBreakdown {
		t.Fatalf("breakdown = %+v, want %+v", health.Breakdown, wantBreakdown)
	}
	if health.Score != 73 {
		t.Fatalf("score = %d, want 73", health.Score)
	}
	if health.Counts.DuplicateNodes != 2 || health.Counts.OrphanNodes != 1 || health.Counts.ChildlessNodes != 2 {
		t.Fatalf("counts = %+v, want 2 duplicates, 1 orphan, 2 childless", health.Counts)
	}
}

// TestBuildInsightsFlagsFiveRules exercises all five red-flag rules and proves
// that the code set is closed and every reason is populated.
func TestBuildInsightsFlagsFiveRules(t *testing.T) {
	vacancy := insightSource("11111111-1111-1111-1111-111111111111", domain.SourceVacancy, "Вакансия")
	interview := insightSource("22222222-2222-2222-2222-222222222222", domain.SourceInterview, "Интервью")

	rootID, functionID, taskID := "n1", "n3", "n4"
	nodes := []domain.Node{
		// Healthy root: meaningful title, strong evidence, two children.
		insightNode(rootID, domain.NodeRole, "Аналитик данных", nil,
			insightEvidenceRecord("e0", interview.ID, "цитата")),
		// Generic title, vacancy-only evidence, no children at all.
		insightNode("n2", domain.NodeFunction, "Функция", insightPtr(rootID),
			insightEvidenceRecord("e1", vacancy.ID, "цитата")),
		// Meaningful function with interview evidence and two task children.
		insightNode(functionID, domain.NodeFunction, "Отчётность и визуализация", insightPtr(rootID),
			insightEvidenceRecord("e2", interview.ID, "цитата")),
		// Generic title, duplicated by n6, but has an action child.
		insightNode(taskID, domain.NodeTask, "Задача", insightPtr(functionID),
			insightEvidenceRecord("e3", interview.ID, "цитата")),
		// Action leaf: never flagged for missing children; meaningful title.
		insightNode("n5", domain.NodeAction, "Проверять отчёты", insightPtr(taskID),
			insightEvidenceRecord("e4", interview.ID, "цитата")),
		// Generic title, duplicated by n4, no children.
		insightNode("n6", domain.NodeTask, "Задача", insightPtr(functionID),
			insightEvidenceRecord("e5", interview.ID, "цитата")),
		// Meaningful title but no evidence: only NO_EVIDENCE.
		insightNode("n7", domain.NodeAction, "Согласовывать метрики", insightPtr(taskID)),
	}

	insights := BuildInsights(nodes, []domain.Source{vacancy, interview}, nil)
	codes := insightCodesByNode(insights.Flags)

	if rootCodes := codes[rootID]; len(rootCodes) != 0 {
		t.Fatalf("root codes = %v, want no flags", rootCodes)
	}
	wantByNode := map[string][]string{
		"n2": {FlagSuspiciousTitle, FlagSingleWeakSource, FlagNoChildren},
		"n4": {FlagSuspiciousTitle, FlagDuplicateTitle},
		"n6": {FlagSuspiciousTitle, FlagDuplicateTitle, FlagNoChildren},
		"n7": {FlagNoEvidence},
	}
	for nodeID, want := range wantByNode {
		got := codes[nodeID]
		if len(got) != len(want) {
			t.Fatalf("%s codes = %v, want %v", nodeID, got, want)
		}
		for index := range want {
			if got[index] != want[index] {
				t.Fatalf("%s codes = %v, want %v (fixed rule order)", nodeID, got, want)
			}
		}
	}
	for _, nodeID := range []string{functionID, "n5"} {
		if got := codes[nodeID]; len(got) != 0 {
			t.Fatalf("%s codes = %v, want no flags", nodeID, got)
		}
	}

	allowed := map[string]struct{}{
		FlagSuspiciousTitle: {}, FlagDuplicateTitle: {}, FlagSingleWeakSource: {},
		FlagNoChildren: {}, FlagNoEvidence: {},
	}
	for _, flag := range insights.Flags {
		if len(flag.Codes) == 0 || len(flag.Codes) != len(flag.Reasons) {
			t.Fatalf("flag %+v: codes and reasons must be parallel and non-empty", flag)
		}
		for index, code := range flag.Codes {
			if _, ok := allowed[code]; !ok {
				t.Fatalf("unexpected flag code %q (the contract has exactly five rules)", code)
			}
			if strings.TrimSpace(flag.Reasons[index]) == "" {
				t.Fatalf("flag %s has an empty reason", code)
			}
		}
	}
}

// TestBuildInsightsRootRules pins the explicit root behaviour: the root is
// not special-cased, so a lone root without evidence and without children gets
// exactly NO_CHILDREN and NO_EVIDENCE (no duplicate/weak/suspicious false
// positives), while a healthy root produces no flags at all.
func TestBuildInsightsRootRules(t *testing.T) {
	bareRoot := insightNode("n1", domain.NodeRole, "Аналитик данных", nil)
	flags := BuildInsights([]domain.Node{bareRoot}, nil, nil).Flags
	if len(flags) != 1 {
		t.Fatalf("bare root flags = %+v, want one aggregate flag", flags)
	}
	wantCodes := []string{FlagNoChildren, FlagNoEvidence}
	if len(flags[0].Codes) != len(wantCodes) {
		t.Fatalf("bare root codes = %v, want %v", flags[0].Codes, wantCodes)
	}
	for index := range wantCodes {
		if flags[0].Codes[index] != wantCodes[index] {
			t.Fatalf("bare root codes = %v, want %v (fixed order)", flags[0].Codes, wantCodes)
		}
	}

	interview := insightSource("11111111-1111-1111-1111-111111111111", domain.SourceInterview, "Интервью")
	rootID := "n1"
	healthyRoot := []domain.Node{
		insightNode(rootID, domain.NodeRole, "Аналитик данных", nil, insightEvidenceRecord("e1", interview.ID, "цитата")),
		insightNode("n2", domain.NodeFunction, "Отчётность и визуализация", insightPtr(rootID), insightEvidenceRecord("e2", interview.ID, "цитата")),
	}
	if healthy := BuildInsights(healthyRoot, []domain.Source{interview}, nil).Flags; len(healthy) != 1 || healthy[0].NodeID != "n2" {
		t.Fatalf("healthy root flags = %+v, want only the childless function n2", healthy)
	}
}

// TestBuildInsightsFlagsHealthyTreeHasNoFalsePositives proves a well-formed
// tree with meaningful titles, strong sources and children produces no flags
// for the root or any other node.
func TestBuildInsightsFlagsHealthyTreeHasNoFalsePositives(t *testing.T) {
	interview := insightSource("11111111-1111-1111-1111-111111111111", domain.SourceInterview, "Интервью")
	standard := insightSource("22222222-2222-2222-2222-222222222222", domain.SourceStandard, "Стандарт")
	inter := func(id string) domain.Evidence { return insightEvidenceRecord(id, interview.ID, "цитата "+id) }
	stan := func(id string) domain.Evidence { return insightEvidenceRecord(id, standard.ID, "цитата "+id) }

	rootID, functionID, taskID := "n1", "n2", "n3"
	nodes := []domain.Node{
		insightNode(rootID, domain.NodeRole, "Аналитик данных", nil, inter("e1"), stan("e2")),
		insightNode(functionID, domain.NodeFunction, "Отчётность и визуализация", insightPtr(rootID), inter("e3"), stan("e4")),
		insightNode(taskID, domain.NodeTask, "Писать SQL-запросы для отчётов", insightPtr(functionID), inter("e5")),
		insightNode("n4", domain.NodeAction, "Согласовывать метрики с заказчиком", insightPtr(taskID), inter("e6")),
		insightNode("n5", domain.NodeAction, "Оптимизировать тяжёлые запросы", insightPtr(taskID), stan("e7")),
	}
	insights := BuildInsights(nodes, []domain.Source{interview, standard}, nil)
	if len(insights.Flags) != 0 {
		t.Fatalf("healthy tree flags = %+v, want none (no false positives, including the root)", insights.Flags)
	}
	if insights.Health.Score != 100 {
		t.Fatalf("healthy tree score = %d, want 100", insights.Health.Score)
	}
}

// TestBuildInsightsSingleWeakSourceRule pins the weak-source threshold:
// interview/standard are strong enough on their own, vacancy/document and
// unknown sources are weak.
func TestBuildInsightsSingleWeakSourceRule(t *testing.T) {
	sources := []domain.Source{
		insightSource("i", domain.SourceInterview, "Интервью"),
		insightSource("s", domain.SourceStandard, "Стандарт"),
		insightSource("v", domain.SourceVacancy, "Вакансия"),
		insightSource("d", domain.SourceDocument, "Документ"),
	}
	cases := []struct {
		name     string
		evidence []domain.Evidence
		want     bool
	}{
		{"interview-only", []domain.Evidence{insightEvidenceRecord("e1", "i", "q")}, false},
		{"standard-only", []domain.Evidence{insightEvidenceRecord("e1", "s", "q")}, false},
		{"vacancy-only", []domain.Evidence{insightEvidenceRecord("e1", "v", "q")}, true},
		{"document-only", []domain.Evidence{insightEvidenceRecord("e1", "d", "q")}, true},
		{"unknown-only", []domain.Evidence{insightEvidenceRecord("e1", "unknown", "q")}, true},
		{"vacancy+interview", []domain.Evidence{
			insightEvidenceRecord("e1", "v", "q1"), insightEvidenceRecord("e2", "i", "q2"),
		}, false},
		{"no-evidence", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node := insightNode("n1", domain.NodeRole, "Аналитик данных", nil, tc.evidence...)
			insights := BuildInsights([]domain.Node{node}, sources, nil)
			got := insightContainsString(insightCodesByNode(insights.Flags)["n1"], FlagSingleWeakSource)
			if got != tc.want {
				t.Fatalf("SINGLE_WEAK_SOURCE = %v, want %v (codes %v)",
					got, tc.want, insightCodesByNode(insights.Flags)["n1"])
			}
		})
	}
}

// TestBuildInsightsSuspiciousTitleRule covers the generic/title rule.
func TestBuildInsightsSuspiciousTitleRule(t *testing.T) {
	cases := []struct {
		title string
		want  bool
	}{
		{"", true},
		{"  ", true},
		{"AB", true},
		{"Роль", true},
		{"role", true},
		{"Заглушка", true},
		{"12345", true},
		{"Писать SQL-запросы", false},
		{"Отчётность и визуализация", false},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			node := insightNode("n1", domain.NodeRole, tc.title, nil, insightEvidenceRecord("e1", "i", "q"))
			insights := BuildInsights([]domain.Node{node}, nil, nil)
			got := insightContainsString(insightCodesByNode(insights.Flags)["n1"], FlagSuspiciousTitle)
			if got != tc.want {
				t.Fatalf("SUSPICIOUS_TITLE for %q = %v, want %v", tc.title, got, tc.want)
			}
		})
	}
}

// TestBuildCoverageMatrix checks row filtering, fixed columns, empty cells and
// per-type evidence counts.
func TestBuildCoverageMatrix(t *testing.T) {
	interview := insightSource("11111111-1111-1111-1111-111111111111", domain.SourceInterview, "Интервью")
	standard := insightSource("22222222-2222-2222-2222-222222222222", domain.SourceStandard, "Стандарт")
	vacancy := insightSource("33333333-3333-3333-3333-333333333333", domain.SourceVacancy, "Вакансия")
	rootID, functionID := "n1", "n2"

	nodes := []domain.Node{
		insightNode(rootID, domain.NodeRole, "Аналитик данных", nil,
			insightEvidenceRecord("e1", interview.ID, "q1"),
			insightEvidenceRecord("e2", standard.ID, "q2"),
		),
		insightNode(functionID, domain.NodeFunction, "Отчётность", insightPtr(rootID),
			insightEvidenceRecord("e3", vacancy.ID, "q3"),
		),
		insightNode("n3", domain.NodeTask, "Писать SQL-запросы", insightPtr(functionID),
			insightEvidenceRecord("e4", interview.ID, "q4"),
			insightEvidenceRecord("e5", "unknown-source", "q5"),
		),
		insightNode("n4", domain.NodeTask, "Проверять качество", insightPtr(functionID)),
		insightNode("n5", domain.NodeAction, "Сверять метрики", insightPtr("n3")),
	}

	coverage := BuildCoverageMatrix(nodes, []domain.Source{interview, standard, vacancy})
	wantColumns := []string{"interview", "standard", "vacancy", "document"}
	if len(coverage.Columns) != len(wantColumns) {
		t.Fatalf("columns = %v, want %v", coverage.Columns, wantColumns)
	}
	for index := range wantColumns {
		if coverage.Columns[index] != wantColumns[index] {
			t.Fatalf("columns = %v, want %v (fixed order)", coverage.Columns, wantColumns)
		}
	}
	if len(coverage.Rows) != 4 {
		t.Fatalf("rows = %d, want 4 role/function/task rows (actions excluded)", len(coverage.Rows))
	}
	cells := func(nodeID string) map[string]int {
		for _, row := range coverage.Rows {
			if row.NodeID == nodeID {
				return row.Cells
			}
		}
		t.Fatalf("coverage row %s is missing", nodeID)
		return nil
	}
	if got := cells(rootID); got["interview"] != 1 || got["standard"] != 1 || got["vacancy"] != 0 || got["document"] != 0 {
		t.Fatalf("root cells = %v", got)
	}
	if got := cells(functionID); got["interview"] != 0 || got["vacancy"] != 1 || got["standard"] != 0 || got["document"] != 0 {
		t.Fatalf("function cells = %v", got)
	}
	if got := cells("n3"); got["interview"] != 1 || got["document"] != 1 || got["standard"] != 0 {
		t.Fatalf("task cells = %v, want unknown source counted as document", got)
	}
	if got := cells("n4"); got["interview"] != 0 || got["standard"] != 0 || got["vacancy"] != 0 || got["document"] != 0 {
		t.Fatalf("empty task cells = %v, want all zero columns present", got)
	}
	for _, row := range coverage.Rows {
		if len(row.Cells) != len(wantColumns) {
			t.Fatalf("row %s has %d cells, want every column present", row.NodeID, len(row.Cells))
		}
	}
}

// TestWriteCoverageCSV checks the exact CSV contract including RFC 4180
// quoting of titles with commas and quotes.
func TestWriteCoverageCSV(t *testing.T) {
	coverage := Coverage{
		Columns: []string{"interview", "standard", "vacancy", "document"},
		Rows: []CoverageRow{
			{NodeID: "n1", Title: "Аналитик данных", Type: "role", Cells: map[string]int{"interview": 2, "standard": 0, "vacancy": 0, "document": 0}},
			{NodeID: "n2", Title: `Писать "SQL", отчёты`, Type: "task", Cells: map[string]int{"interview": 0, "standard": 1, "vacancy": 0, "document": 2}},
		},
	}
	var buffer bytes.Buffer
	if err := WriteCoverageCSV(&buffer, coverage); err != nil {
		t.Fatalf("WriteCoverageCSV: %v", err)
	}
	want := "node_id,title,type,interview,standard,vacancy,document\n" +
		"n1,Аналитик данных,role,2,0,0,0\n" +
		"n2,\"Писать \"\"SQL\"\", отчёты\",task,0,1,0,2\n"
	if buffer.String() != want {
		t.Fatalf("csv =\n%q\nwant\n%q", buffer.String(), want)
	}
}

// TestBuildInsightsMarketHeat checks case-insensitive token matching, duplicate
// spelling merging, ZUN coverage, top-10 limiting and the empty state.
func TestBuildInsightsMarketHeat(t *testing.T) {
	nodes := []domain.Node{
		insightNode("n1", domain.NodeRole, "Писать SQL-запросы", nil),
		insightNode("n2", domain.NodeFunction, "Report about R language", nil),
		insightNode("n3", domain.NodeTask, "PostgreSQL и Python", nil),
		insightNode("n4", domain.NodeAction, "Отчётность", nil),
		{
			ID: "n5", Type: domain.NodeTask, Title: "Аналитик",
			Zun: &domain.Zun{Skills: []string{"SQL"}, Knowledge: []string{"python"}},
		},
	}
	heat := BuildInsights(nodes, nil, map[string]int{
		"SQL":    42,
		"sql":    8,
		"python": 10,
		"R":      5,
		"zero":   0,
	}).MarketHeat

	if heat.Source != "run.skill_freq" {
		t.Fatalf("source = %q, want run.skill_freq", heat.Source)
	}
	wantTop := []MarketSkill{{Skill: "SQL", Frequency: 50}, {Skill: "python", Frequency: 10}, {Skill: "R", Frequency: 5}}
	if len(heat.Top) != len(wantTop) {
		t.Fatalf("top = %+v, want %+v", heat.Top, wantTop)
	}
	for index := range wantTop {
		if heat.Top[index] != wantTop[index] {
			t.Fatalf("top = %+v, want %+v", heat.Top, wantTop)
		}
	}
	if got := heat.Nodes["n1"]; got.Score != 50 || len(got.Matches) != 1 || got.Matches[0] != "SQL" {
		t.Fatalf("n1 heat = %+v, want SQL 50", got)
	}
	if got := heat.Nodes["n2"]; got.Score != 5 || len(got.Matches) != 1 || got.Matches[0] != "R" {
		t.Fatalf("n2 heat = %+v, want R 5 (R must not match 'Report')", got)
	}
	if _, ok := heat.Nodes["n4"]; ok {
		t.Fatalf("n4 must not have market heat: %+v", heat.Nodes["n4"])
	}
	if got := heat.Nodes["n5"]; got.Score != 60 || len(got.Matches) != 2 {
		t.Fatalf("n5 heat = %+v, want ZUN matches summing to 60", got)
	}

	empty := BuildInsights(nodes, nil, nil).MarketHeat
	if empty.Source != "none" || empty.Top == nil || len(empty.Top) != 0 || empty.Nodes == nil || len(empty.Nodes) != 0 {
		t.Fatalf("empty market heat = %+v, want none with non-nil empty collections", empty)
	}
}

// TestBuildInsightsMarketHeatTopLimit verifies the top-10 cap and deterministic
// ordering for tied frequencies.
func TestBuildInsightsMarketHeatTopLimit(t *testing.T) {
	skillFreq := map[string]int{}
	for index := 0; index < 12; index++ {
		skillFreq["skill-"+string(rune('a'+index))] = 1
	}
	heat := BuildInsights(nil, nil, skillFreq).MarketHeat
	if len(heat.Top) != 10 {
		t.Fatalf("top length = %d, want 10", len(heat.Top))
	}
	if heat.Top[0].Skill != "skill-a" || heat.Top[9].Skill != "skill-j" {
		t.Fatalf("top = %+v, want alphabetical order for tied frequencies", heat.Top)
	}
}

// TestBuildInsightsNodeExplanations checks that every node gets a template
// explanation with source type/priority, including the empty-evidence state.
func TestBuildInsightsNodeExplanations(t *testing.T) {
	interview := insightSource("11111111-1111-1111-1111-111111111111", domain.SourceInterview, "Интервью")
	rootID := "n1"
	nodes := []domain.Node{
		insightNode(rootID, domain.NodeRole, "Аналитик данных", nil,
			domain.Evidence{ID: "e1", SourceID: interview.ID, RefID: "ref-1", Quote: "цитата 1", Status: domain.EvidenceApproved},
			insightEvidenceRecord("e2", "unknown-source", "цитата 2"),
		),
		insightNode("n2", domain.NodeFunction, "Отчётность", insightPtr(rootID)),
	}
	explanations := BuildInsights(nodes, []domain.Source{interview}, nil).NodeExplanations
	if len(explanations) != len(nodes) {
		t.Fatalf("explanations = %d, want one per node (%d)", len(explanations), len(nodes))
	}
	rootExplanation := explanations[rootID]
	if strings.TrimSpace(rootExplanation.Summary) == "" {
		t.Fatal("root explanation summary is empty")
	}
	if !strings.Contains(rootExplanation.Summary, "interview") || !strings.Contains(rootExplanation.Summary, "300") {
		t.Fatalf("root summary = %q, want source type and priority", rootExplanation.Summary)
	}
	if len(rootExplanation.Evidence) != 2 {
		t.Fatalf("root explanation evidence = %d, want 2", len(rootExplanation.Evidence))
	}
	if rootExplanation.Evidence[0].SourceType != "interview" || rootExplanation.Evidence[0].Priority != 300 {
		t.Fatalf("root evidence[0] = %+v, want interview/300", rootExplanation.Evidence[0])
	}
	if rootExplanation.Evidence[0].RefID != "ref-1" || rootExplanation.Evidence[0].Status != string(domain.EvidenceApproved) {
		t.Fatalf("root evidence[0] = %+v, want ref_id/status preserved", rootExplanation.Evidence[0])
	}
	if rootExplanation.Evidence[1].SourceType != "document" || rootExplanation.Evidence[1].Priority != 50 {
		t.Fatalf("root evidence[1] = %+v, want unknown source as document/50", rootExplanation.Evidence[1])
	}

	noEvidence := explanations["n2"]
	if !strings.Contains(noEvidence.Summary, "не подтверждён") {
		t.Fatalf("no-evidence summary = %q, want the missing-data state", noEvidence.Summary)
	}
	if noEvidence.Evidence == nil || len(noEvidence.Evidence) != 0 {
		t.Fatalf("no-evidence list = %#v, want non-nil empty slice", noEvidence.Evidence)
	}
}

// TestMarketSkillFrequencyFromSources covers the deterministic fallback
// extractor: only vacancy/standard sources with content are scanned, the
// frequency is the number of documents containing the skill, aliases are
// canonicalized, "ё" spelling is normalized and the sample size counts the
// scanned documents.
func TestMarketSkillFrequencyFromSources(t *testing.T) {
	sources := []domain.Source{
		{ID: "v1", Type: domain.SourceVacancy, Content: "We need SQL, Python and dashboards."},
		{ID: "s1", Type: domain.SourceStandard, Content: "Отчётность и визуализация. SQL."},
		{ID: "i1", Type: domain.SourceInterview, Content: "SQL everywhere in the interview"},
		{ID: "d1", Type: domain.SourceDocument, Content: "SQL in a document"},
		{ID: "v2", Type: domain.SourceVacancy, Content: "  "},
	}
	freq, sampleSize := MarketSkillFrequencyFromSources(sources)
	if sampleSize != 2 {
		t.Fatalf("sample size = %d, want 2 (vacancy+standard with content)", sampleSize)
	}
	if freq["sql"] != 2 {
		t.Fatalf("sql frequency = %d, want 2 (two documents contain it)", freq["sql"])
	}
	if freq["python"] != 1 {
		t.Fatalf("python frequency = %d, want 1", freq["python"])
	}
	if freq["dashboards"] != 1 {
		t.Fatalf("dashboards frequency = %d, want 1", freq["dashboards"])
	}
	// "Отчётность" must match the "отчетность" catalog term despite the "ё".
	if freq["отчетность"] != 1 {
		t.Fatalf("отчетность frequency = %d, want 1 (ё normalized)", freq["отчетность"])
	}
	if _, ok := freq["python"]; !ok {
		t.Fatalf("frequency map = %v, want lowercase canonical keys", freq)
	}

	emptyFreq, emptySample := MarketSkillFrequencyFromSources(nil)
	if emptySample != 0 || emptyFreq == nil || len(emptyFreq) != 0 {
		t.Fatalf("nil sources = (%v, %d), want non-nil empty map and 0", emptyFreq, emptySample)
	}
	interviewOnly, interviewSample := MarketSkillFrequencyFromSources([]domain.Source{
		{ID: "i1", Type: domain.SourceInterview, Content: "SQL"},
	})
	if interviewSample != 0 || len(interviewOnly) != 0 {
		t.Fatalf("interview-only = (%v, %d), want no data", interviewOnly, interviewSample)
	}
}

// TestBuildInsightsMarketFallback verifies the fallback source label,
// sample_size and node matching, and that run.SkillFreq takes precedence.
func TestBuildInsightsMarketFallback(t *testing.T) {
	nodes := []domain.Node{
		insightNode("n1", domain.NodeRole, "Аналитик данных", nil),
		insightNode("n2", domain.NodeTask, "Писать SQL-запросы", insightPtr("n1")),
		insightNode("n3", domain.NodeTask, "Строить дашборды в BI", insightPtr("n1")),
	}
	market := MarketInput{
		FallbackFreq:       map[string]int{"sql": 2, "дашборды": 1},
		FallbackSampleSize: 2,
	}
	heat := BuildInsightsWithMarket(nodes, nil, market).MarketHeat
	if heat.Source != "vacancy_sources" || heat.SampleSize != 2 {
		t.Fatalf("fallback heat = source %q sample %d, want vacancy_sources/2", heat.Source, heat.SampleSize)
	}
	if len(heat.Top) != 2 || heat.Top[0].Skill != "sql" || heat.Top[0].Frequency != 2 {
		t.Fatalf("fallback top = %+v, want sql/2 first", heat.Top)
	}
	if got := heat.Nodes["n2"]; got.Score != 2 || len(got.Matches) != 1 || got.Matches[0] != "sql" {
		t.Fatalf("n2 heat = %+v, want sql/2", got)
	}
	if got := heat.Nodes["n3"]; got.Score != 1 || got.Matches[0] != "дашборды" {
		t.Fatalf("n3 heat = %+v, want дашборды/1", got)
	}
	if _, ok := heat.Nodes["n1"]; ok {
		t.Fatalf("n1 must not have fallback heat: %+v", heat.Nodes["n1"])
	}

	precedence := BuildInsightsWithMarket(nodes, nil, MarketInput{
		SkillFreq:          map[string]int{"Go": 7},
		FallbackFreq:       map[string]int{"sql": 2},
		FallbackSampleSize: 2,
	}).MarketHeat
	if precedence.Source != "run.skill_freq" {
		t.Fatalf("precedence source = %q, want run.skill_freq", precedence.Source)
	}
	if precedence.SampleSize != 0 {
		t.Fatalf("precedence sample size = %d, want 0 (omitted)", precedence.SampleSize)
	}
	if len(precedence.Top) != 1 || precedence.Top[0].Skill != "Go" || precedence.Top[0].Frequency != 7 {
		t.Fatalf("precedence top = %+v, want Go/7", precedence.Top)
	}

	noData := BuildInsightsWithMarket(nodes, nil, MarketInput{}).MarketHeat
	if noData.Source != "none" || noData.SampleSize != 0 || len(noData.Top) != 0 || len(noData.Nodes) != 0 {
		t.Fatalf("no-data heat = %+v, want none/empty", noData)
	}
}

// TestBuildInsightsMarketFallbackTopLimit verifies the top-10 cap and
// deterministic ordering for the source-content fallback too.
func TestBuildInsightsMarketFallbackTopLimit(t *testing.T) {
	fallbackFreq := map[string]int{}
	for index := 0; index < 12; index++ {
		fallbackFreq["skill-"+string(rune('a'+index))] = 3
	}
	heat := BuildInsightsWithMarket(nil, nil, MarketInput{
		FallbackFreq:       fallbackFreq,
		FallbackSampleSize: 4,
	}).MarketHeat
	if heat.Source != "vacancy_sources" || heat.SampleSize != 4 {
		t.Fatalf("heat = %+v, want vacancy_sources/4", heat)
	}
	if len(heat.Top) != 10 {
		t.Fatalf("fallback top length = %d, want 10", len(heat.Top))
	}
	if heat.Top[0].Skill != "skill-a" || heat.Top[9].Skill != "skill-j" {
		t.Fatalf("fallback top = %+v, want alphabetical order for tied frequencies", heat.Top)
	}
}
