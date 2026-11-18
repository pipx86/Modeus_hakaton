package analyst

import (
	"context"
	"errors"
	"strings"
	"testing"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/domain"
)

const (
	testStandardContent = "Профессиональный стандарт аналитика данных. Трудовая задача: писать SQL-запросы для отчётов."
	testVacancyContent  = "Вакансия Data Analyst. Обязанности: строить дашборды и проверять выгрузки."
)

func testMultiSourceInput(t *testing.T) agent.AnalystInput {
	t.Helper()
	return agent.AnalystInput{
		SessionID:  "sess-multi",
		Profession: "Аналитик данных",
		Transcript: "Вопрос: Что вы делаете?\nОтвет: Каждый день пишу SQL-запросы.\n",
		Sources: []domain.Source{
			{ID: "src-standard-1", Type: domain.SourceStandard, Title: "Профстандарт", Content: testStandardContent},
			{ID: "src-vacancy-1", Type: domain.SourceVacancy, Title: "Вакансия", Content: testVacancyContent},
		},
	}
}

func TestValidateEntitiesWithSourcesAcceptsSourceQuotes(t *testing.T) {
	in := testMultiSourceInput(t)
	valid := agent.AnalystOutput{Entities: []agent.ExtractedEntity{{
		Node: domain.Node{ID: "task-1", Type: domain.NodeTask, Title: "Писать SQL-запросы",
			Evidence: []domain.Evidence{{ID: "ev-1", SourceID: "src-standard-1", RefID: "req-1", Quote: "писать SQL-запросы"}}},
	}}}
	if err := ValidateEntitiesWithSources(valid, in.Transcript, in.Sources); err != nil {
		t.Fatalf("quote from standard source rejected: %v", err)
	}

	missing := valid
	missing.Entities = []agent.ExtractedEntity{{
		Node: domain.Node{ID: "task-2", Type: domain.NodeTask, Title: "Писать Python",
			Evidence: []domain.Evidence{{ID: "ev-2", SourceID: "src-standard-1", RefID: "req-2", Quote: "писать Python"}}},
	}}
	if err := ValidateEntitiesWithSources(missing, in.Transcript, in.Sources); !errors.Is(err, ErrQuoteMismatch) {
		t.Fatalf("quote from nowhere: err = %v, want ErrQuoteMismatch", err)
	}
}

func TestResolveEvidenceSourcesReattributesQuotes(t *testing.T) {
	in := testMultiSourceInput(t)
	out := agent.AnalystOutput{Entities: []agent.ExtractedEntity{{
		Node: domain.Node{ID: "task-1", Type: domain.NodeTask, Title: "Писать SQL-запросы",
			Evidence: []domain.Evidence{
				// Model invented an id instead of citing the standard source.
				{ID: "ev-1", SourceID: "invented-1", RefID: "req-1", Quote: "писать SQL-запросы"},
				// Interview quote wrongly attributed to the vacancy source.
				{ID: "ev-2", SourceID: "src-vacancy-1", RefID: "msg-1", Quote: "Каждый день пишу SQL-запросы"},
			}},
	}}}
	if err := ResolveEvidenceSources(&out, in); err != nil {
		t.Fatalf("ResolveEvidenceSources: %v", err)
	}
	got := out.Entities[0].Evidence
	if got[0].SourceID != "src-standard-1" {
		t.Fatalf("unknown id was not reattributed to the standard source: %+v", got[0])
	}
	if got[1].SourceID != InterviewSourceID {
		t.Fatalf("interview quote was not normalized to %q: %+v", InterviewSourceID, got[1])
	}
}

// TestAnalyzeKeepsConflictingSourceTitles verifies the central multi-source
// property: two entities describing the same role from a standard and a vacancy
// source keep their own title and evidence, and share the taxonomy id, so the
// deterministic synthesizer can detect the conflict later.
func TestAnalyzeKeepsConflictingSourceTitles(t *testing.T) {
	response := `{"entities":[
	 {"id":"std-role","type":"role","title":"Аналитик данных","confidence":0.9,
	  "evidence":[{"id":"e1","source_id":"src-standard-1","ref_id":"req-0","quote":"Профессиональный стандарт аналитика данных"}]},
	 {"id":"vac-role","type":"role","title":"Data Analyst","confidence":0.8,
	  "evidence":[{"id":"e2","source_id":"src-vacancy-1","ref_id":"line-0","quote":"Вакансия Data Analyst"}]}
	]}`
	client := &fakeClient{t: t, responses: []string{response}}
	analyst, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := analyst.Analyze(context.Background(), testMultiSourceInput(t))
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	roles := make([]domain.Node, 0, 2)
	for _, entity := range out.Entities {
		if entity.Type == domain.NodeRole {
			roles = append(roles, entity.Node)
		}
	}
	if len(roles) != 2 {
		t.Fatalf("role nodes = %d (%+v), want one per source", len(roles), out.Entities)
	}
	if roles[0].ID != roles[1].ID {
		t.Fatalf("taxonomy did not unify conflicting role ids: %q vs %q", roles[0].ID, roles[1].ID)
	}
	if roles[0].Title == roles[1].Title {
		t.Fatalf("source titles were merged: %+v", roles)
	}
	for _, role := range roles {
		if len(role.Evidence) != 1 {
			t.Fatalf("role %q evidence = %+v, want exactly its own source quote", role.Title, role.Evidence)
		}
	}
}

// TestAnalyzeSplitsMixedEvidenceByProvenance verifies that a model answer that
// packs standard and vacancy quotes into one entity is split into two
// single-provenance entities instead of collapsing into Interview.
func TestAnalyzeSplitsMixedEvidenceByProvenance(t *testing.T) {
	response := `{"entities":[
	 {"id":"task-mixed","type":"task","title":"Настраивать CI/CD",
	  "evidence":[
	   {"id":"e1","source_id":"src-standard-1","ref_id":"req-9","quote":"писать SQL-запросы"},
	   {"id":"e2","source_id":"src-vacancy-1","ref_id":"line-9","quote":"строить дашборды"}
	  ]}
	]}`
	client := &fakeClient{t: t, responses: []string{response}}
	analyst, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := analyst.Analyze(context.Background(), testMultiSourceInput(t))
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(out.Entities) != 2 {
		t.Fatalf("entities = %+v, want split per source", out.Entities)
	}
	for _, entity := range out.Entities {
		if len(entity.Evidence) != 1 {
			t.Fatalf("entity %q evidence = %+v, want one", entity.Title, entity.Evidence)
		}
		sourceID := entity.Evidence[0].SourceID
		if sourceID != "src-standard-1" && sourceID != "src-vacancy-1" {
			t.Fatalf("entity %q evidence source = %q", entity.Title, sourceID)
		}
	}
	if out.Entities[0].ID != out.Entities[1].ID {
		t.Fatalf("split entities lost the shared id: %q vs %q", out.Entities[0].ID, out.Entities[1].ID)
	}
}

// TestExtractRepairsQuoteFromUnknownSource verifies that a quote found in
// neither the transcript nor the declared sources triggers the repair loop.
func TestExtractRepairsQuoteFromUnknownSource(t *testing.T) {
	bogus := `{"entities":[{"id":"a1","type":"task","title":"Писать SQL-запросы",
	  "evidence":[{"id":"e1","source_id":"src-standard-1","ref_id":"req-1","quote":"этой фразы нет нигде"}]}]}`
	valid := `{"entities":[{"id":"a1","type":"task","title":"Писать SQL-запросы",
	  "evidence":[{"id":"e1","source_id":"src-standard-1","ref_id":"req-1","quote":"писать SQL-запросы"}]}]}`
	in := testMultiSourceInput(t)
	client := &fakeClient{t: t, responses: []string{bogus, valid}}
	analyst, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := analyst.Extract(context.Background(), in)
	if err != nil {
		t.Fatalf("Extract after repair: %v", err)
	}
	if client.calls != 2 {
		t.Fatalf("model calls = %d, want 2 (invalid quote + repaired answer)", client.calls)
	}
	if len(out.Entities) != 1 || out.Entities[0].Evidence[0].SourceID != "src-standard-1" {
		t.Fatalf("resolved output = %+v", out.Entities)
	}
}

func TestSplitEntitiesByProvenanceOrdersGroups(t *testing.T) {
	entities := []agent.ExtractedEntity{
		{Node: domain.Node{ID: "interview", Type: domain.NodeTask, Title: "Interview", Evidence: []domain.Evidence{
			{ID: "e1", SourceID: "unknown", RefID: "r", Quote: "q"},
		}}},
		{Node: domain.Node{ID: "vacancy", Type: domain.NodeTask, Title: "Vacancy", Evidence: []domain.Evidence{
			{ID: "e2", SourceID: "src-vacancy-1", RefID: "r", Quote: "q"},
		}}},
		{Node: domain.Node{ID: "standard", Type: domain.NodeTask, Title: "Standard", Evidence: []domain.Evidence{
			{ID: "e3", SourceID: "src-standard-1", RefID: "r", Quote: "q"},
		}}},
	}
	groups := splitEntitiesByProvenance(entities, testMultiSourceInput(t).Sources)
	if len(groups) != 3 {
		t.Fatalf("groups = %d, want 3", len(groups))
	}
	want := []domain.SourceType{domain.SourceInterview, domain.SourceStandard, domain.SourceVacancy}
	for i, group := range groups {
		if group.class != want[i] {
			t.Fatalf("group %d class = %q, want %q", i, group.class, want[i])
		}
	}
	if strings.TrimSpace(groups[1].entities[0].Evidence[0].SourceID) != "src-standard-1" {
		t.Fatalf("standard group evidence = %+v", groups[1].entities)
	}
}

// TestAnalyzeRunsFocusedSourceExtraction reproduces the live-model failure mode:
// the main extraction only quotes the interview. The analyst must then run one
// focused pass per material source, force the real source id and keep the
// conflicting source titles on the shared taxonomy id.
func TestAnalyzeRunsFocusedSourceExtraction(t *testing.T) {
	mainAnswer := `{"entities":[
	 {"id":"tax-task-sql","type":"task","title":"Писать SQL-запросы","confidence":0.9,
	  "evidence":[{"id":"e1","source_id":"interview-transcript","ref_id":"a1","quote":"Каждый день пишу SQL-запросы"}]}
	]}`
	standardAnswer := `{"entities":[
	 {"id":"std-role","type":"role","title":"Аналитик данных","confidence":0.9,
	  "evidence":[{"id":"e2","source_id":"ignored-by-test","ref_id":"line-1","quote":"Профессиональный стандарт аналитика данных"}]}
	]}`
	vacancyAnswer := `{"entities":[
	 {"id":"vac-role","type":"role","title":"Data Analyst","confidence":0.8,
	  "evidence":[{"id":"e3","source_id":"ignored-by-test","ref_id":"line-1","quote":"Вакансия Data Analyst"}]}
	]}`
	client := &fakeClient{t: t, responses: []string{mainAnswer, standardAnswer, vacancyAnswer}}
	analyst, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := analyst.Analyze(context.Background(), testMultiSourceInput(t))
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if client.calls != 3 {
		t.Fatalf("model calls = %d, want main + one focused pass per source", client.calls)
	}
	roles := make([]domain.Node, 0, 2)
	for _, entity := range out.Entities {
		if entity.Type == domain.NodeRole {
			roles = append(roles, entity.Node)
		}
	}
	// The taxonomy adds an interview role ancestor as well, so select the roles
	// by the source their evidence actually cites.
	var standard, vacancy *domain.Node
	for i := range roles {
		if len(roles[i].Evidence) == 0 {
			continue
		}
		switch roles[i].Evidence[0].SourceID {
		case "src-standard-1":
			standard = &roles[i]
		case "src-vacancy-1":
			vacancy = &roles[i]
		}
	}
	if standard == nil || vacancy == nil {
		t.Fatalf("source roles missing: %+v", roles)
	}
	if standard.ID != vacancy.ID {
		t.Fatalf("taxonomy did not unify source roles: %q vs %q", standard.ID, vacancy.ID)
	}
	if standard.Title != "Аналитик данных" || vacancy.Title != "Data Analyst" {
		t.Fatalf("source titles were lost: %q / %q", standard.Title, vacancy.Title)
	}
	if standard.Evidence[0].SourceID != "src-standard-1" || vacancy.Evidence[0].SourceID != "src-vacancy-1" {
		t.Fatalf("evidence sources = %q / %q", standard.Evidence[0].SourceID, vacancy.Evidence[0].SourceID)
	}
}

// TestAnalyzeFocusedSourceFailureKeepsInterviewResult verifies that a provider
// failure in a focused source pass does not discard the valid interview result.
func TestAnalyzeFocusedSourceFailureKeepsInterviewResult(t *testing.T) {
	mainAnswer := `{"entities":[
	 {"id":"tax-task-sql","type":"task","title":"Писать SQL-запросы","confidence":0.9,
	  "evidence":[{"id":"e1","source_id":"interview-transcript","ref_id":"a1","quote":"Каждый день пишу SQL-запросы"}]}
	]}`
	client := &fakeClient{t: t, responses: []string{mainAnswer, "not json", "not json"}}
	analyst, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := analyst.Analyze(context.Background(), testMultiSourceInput(t))
	if err != nil {
		t.Fatalf("Analyze = %v, want interview result", err)
	}
	if len(out.Entities) == 0 {
		t.Fatal("interview entities were dropped after source pass failure")
	}
}
