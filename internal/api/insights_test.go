package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"activity-descriptor/internal/analysis"
	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	insightsTestRunID       = "11111111-1111-1111-1111-111111111111"
	insightsTestInterviewID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	insightsTestStandardID  = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	insightsTestVacancyID   = "cccccccc-cccc-cccc-cccc-cccccccccccc"
	insightsTestUnknownID   = "dddddddd-dddd-dddd-dddd-dddddddddddd"
)

// insightsFakeStore is an in-memory insightsReader wired into Server through
// the Store interface (the embedded nil Store panics loudly if a handler calls
// a method this test does not need).
type insightsFakeStore struct {
	Store
	run           *domain.Run
	nodes         []domain.Node
	sources       []domain.Source
	contents      []domain.Source
	runErr        error
	treeErr       error
	sourcesErr    error
	contentsErr   error
	contentsCalls int
}

func (f *insightsFakeStore) GetRun(id string) (*domain.Run, error) {
	if f.runErr != nil {
		return nil, f.runErr
	}
	if f.run == nil || f.run.ID != id {
		return nil, ErrNotFound
	}
	return f.run, nil
}

func (f *insightsFakeStore) GetTree(runID string) ([]domain.Node, error) {
	if f.treeErr != nil {
		return nil, f.treeErr
	}
	if f.run == nil || f.run.ID != runID {
		return nil, ErrNotFound
	}
	return f.nodes, nil
}

func (f *insightsFakeStore) ListRunSources(_ context.Context, runID string) ([]domain.Source, error) {
	if f.sourcesErr != nil {
		return nil, f.sourcesErr
	}
	if f.run == nil || f.run.ID != runID {
		return nil, ErrNotFound
	}
	return f.sources, nil
}

// ListRunSourceContents makes the fake satisfy the optional
// runSourceContentReader capability used by the market-heat fallback.
func (f *insightsFakeStore) ListRunSourceContents(_ context.Context, runID string) ([]domain.Source, error) {
	f.contentsCalls++
	if f.contentsErr != nil {
		return nil, f.contentsErr
	}
	if f.run == nil || f.run.ID != runID {
		return nil, ErrNotFound
	}
	return f.contents, nil
}

// insightsTestHandler mounts the two new handlers on a throwaway mux behind the
// real auth middleware. The production route registration happens in
// Server.Routes (owned by the lead); this mirrors it for handler-level tests.
func insightsTestHandler(store Store) http.Handler {
	server := NewServer(store)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/runs/{id}/insights", server.handleRunInsights)
	mux.HandleFunc("GET /api/v1/runs/{id}/coverage", server.handleRunCoverage)
	return auth.Middleware(mux)
}

func insightsTestNode(id string, nodeType domain.NodeType, title string, parentID *string, evidence ...domain.Evidence) domain.Node {
	return domain.Node{ID: id, Type: nodeType, Title: title, ParentID: parentID, Evidence: evidence}
}

func insightsTestPtr(value string) *string { return &value }

func insightsTestEvidence(id, sourceID, quote string) domain.Evidence {
	return domain.Evidence{ID: id, SourceID: sourceID, Quote: quote}
}

// insightsTestFixture returns one healthy run with all four source types
// represented: a role grounded by interview/standard/unknown evidence (the
// unknown id exercises the document fallback), a vacancy-only function that
// must raise SINGLE_WEAK_SOURCE, a SQL task (market-heat match) and an action.
func insightsTestFixture() *insightsFakeStore {
	rootID, functionID, taskID := "n1", "n2", "n3"
	evidence := func(id, sourceID, quote string) domain.Evidence {
		return insightsTestEvidence(id, sourceID, quote)
	}
	nodes := []domain.Node{
		insightsTestNode(rootID, domain.NodeRole, "Аналитик данных", nil,
			evidence("e1", insightsTestInterviewID, "Пишу SQL-запросы"),
			evidence("e2", insightsTestStandardID, "Отчётность и визуализация"),
			evidence("e1", insightsTestInterviewID, "Пишу SQL-запросы"), // duplicate id, must not double count
			evidence("e6", insightsTestUnknownID, "цитата из неучтённого источника"),
		),
		insightsTestNode(functionID, domain.NodeFunction, "Отчётность и визуализация", insightsTestPtr(rootID),
			evidence("e3", insightsTestVacancyID, "Строить отчёты"),
		),
		insightsTestNode(taskID, domain.NodeTask, "Писать SQL-запросы", insightsTestPtr(functionID),
			evidence("e4", insightsTestInterviewID, "SQL-запросы каждый день"),
		),
		insightsTestNode("n4", domain.NodeAction, "Проверять выгрузки", insightsTestPtr(taskID),
			evidence("e5", insightsTestInterviewID, "Проверяю выгрузки"),
		),
	}
	return &insightsFakeStore{
		run: &domain.Run{
			ID:         insightsTestRunID,
			Profession: "Аналитик данных",
			Status:     domain.RunDone,
			Stage:      domain.StageDone,
			SkillFreq:  map[string]int{"SQL": 42},
		},
		nodes: nodes,
		sources: []domain.Source{
			{ID: insightsTestInterviewID, Type: domain.SourceInterview, Title: "Interview transcript"},
			{ID: insightsTestStandardID, Type: domain.SourceStandard, Title: "Профстандарт"},
			{ID: insightsTestVacancyID, Type: domain.SourceVacancy, Title: "Вакансия"},
		},
		// Contents exist so the precedence test can prove they are not read
		// while Run.SkillFreq is present.
		contents: []domain.Source{
			{ID: insightsTestVacancyID, Type: domain.SourceVacancy, Title: "Вакансия", Content: "Go, Kubernetes"},
		},
	}
}

// insightsTestMarketFixture builds a run without Run.SkillFreq plus vacancy and
// standard source contents for the deterministic market-heat fallback.
func insightsTestMarketFixture() *insightsFakeStore {
	rootID, functionID, taskID := "n1", "n2", "n3"
	nodes := []domain.Node{
		insightsTestNode(rootID, domain.NodeRole, "Аналитик данных", nil,
			insightsTestEvidence("e1", insightsTestInterviewID, "цитата")),
		insightsTestNode(functionID, domain.NodeFunction, "Отчётность и визуализация", insightsTestPtr(rootID),
			insightsTestEvidence("e2", insightsTestStandardID, "цитата")),
		insightsTestNode(taskID, domain.NodeTask, "Писать SQL-запросы", insightsTestPtr(functionID),
			insightsTestEvidence("e3", insightsTestInterviewID, "цитата")),
	}
	return &insightsFakeStore{
		run: &domain.Run{
			ID:         insightsTestRunID,
			Profession: "Аналитик данных",
			Status:     domain.RunDone,
			Stage:      domain.StageDone,
		},
		nodes: nodes,
		sources: []domain.Source{
			{ID: insightsTestInterviewID, Type: domain.SourceInterview, Title: "Interview transcript"},
			{ID: insightsTestStandardID, Type: domain.SourceStandard, Title: "Профстандарт"},
			{ID: insightsTestVacancyID, Type: domain.SourceVacancy, Title: "Вакансия"},
		},
		contents: []domain.Source{
			{ID: insightsTestVacancyID, Type: domain.SourceVacancy, Title: "Вакансия",
				Content: "Обязанности: писать SQL-запросы, Python, дашборды."},
			{ID: insightsTestStandardID, Type: domain.SourceStandard, Title: "Профстандарт",
				Content: "Отчётность и визуализация. SQL."},
			{ID: insightsTestInterviewID, Type: domain.SourceInterview, Title: "Interview transcript",
				Content: "SQL"},
		},
	}
}

// TestRunInsightsHandlerContract checks the aggregate payload over HTTP.
func TestRunInsightsHandlerContract(t *testing.T) {
	fixture := insightsTestFixture()
	handler := insightsTestHandler(fixture)
	response := do(t, handler, http.MethodGet, "/api/v1/runs/"+insightsTestRunID+"/insights", "teacher", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", response.Code, response.Body.String())
	}
	var payload analysis.Insights
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("insights JSON: %v (%s)", err, response.Body.String())
	}
	if payload.RunID != insightsTestRunID {
		t.Fatalf("run_id = %q, want %q", payload.RunID, insightsTestRunID)
	}

	// Feature #1: provenance (duplicate evidence collapsed, unknown source
	// counted as a document).
	rootProvenance := payload.Provenance.Nodes["n1"]
	if rootProvenance.SourceCount != 3 || rootProvenance.EvidenceCount != 3 {
		t.Fatalf("root provenance = %+v, want source_count 3, evidence_count 3", rootProvenance)
	}
	if want := []string{"interview", "standard", "document"}; strings.Join(rootProvenance.SourceTypes, ",") != strings.Join(want, ",") {
		t.Fatalf("root source types = %v, want %v", rootProvenance.SourceTypes, want)
	}

	// Feature #3: health.
	if payload.Health.Score != 100 {
		t.Fatalf("health score = %d, want 100 (breakdown %+v)", payload.Health.Score, payload.Health.Breakdown)
	}
	if payload.Health.Formula == "" || payload.Health.Weights["groundedness"] != 0.40 {
		t.Fatalf("health formula/weights = %+v", payload.Health)
	}

	// Feature #5: exactly one weak-source flag (the vacancy-only function).
	if len(payload.Flags) != 1 || payload.Flags[0].NodeID != "n2" {
		t.Fatalf("flags = %+v, want only the vacancy-only function", payload.Flags)
	}
	if len(payload.Flags[0].Codes) != 1 || payload.Flags[0].Codes[0] != analysis.FlagSingleWeakSource {
		t.Fatalf("function flags = %+v, want SINGLE_WEAK_SOURCE", payload.Flags[0])
	}
	if strings.TrimSpace(payload.Flags[0].Reasons[0]) == "" {
		t.Fatal("flag reason is empty")
	}

	// Feature #8: market heat from run.SkillFreq.
	if payload.MarketHeat.Source != "run.skill_freq" || len(payload.MarketHeat.Top) != 1 ||
		payload.MarketHeat.Top[0].Skill != "SQL" || payload.MarketHeat.Top[0].Frequency != 42 {
		t.Fatalf("market heat top = %+v, want SQL/42", payload.MarketHeat.Top)
	}
	if payload.MarketHeat.SampleSize != 0 {
		t.Fatalf("run.skill_freq sample_size = %d, want 0 (omitted)", payload.MarketHeat.SampleSize)
	}
	if got := payload.MarketHeat.Nodes["n3"]; got.Score != 42 || len(got.Matches) != 1 || got.Matches[0] != "SQL" {
		t.Fatalf("task market heat = %+v, want SQL 42", got)
	}
	// run.SkillFreq takes precedence: source contents must not be read at all.
	if fixture.contentsCalls != 0 {
		t.Fatalf("source contents read %d times, want 0 while run.SkillFreq is present", fixture.contentsCalls)
	}

	// Feature #9: coverage matrix rows are role/function/task only.
	if len(payload.Coverage.Columns) != 4 {
		t.Fatalf("coverage columns = %v, want four fixed columns", payload.Coverage.Columns)
	}
	if len(payload.Coverage.Rows) != 3 {
		t.Fatalf("coverage rows = %d, want 3 (action excluded)", len(payload.Coverage.Rows))
	}
	cells := map[string]map[string]int{}
	for _, row := range payload.Coverage.Rows {
		cells[row.NodeID] = row.Cells
	}
	if got := cells["n1"]; got["interview"] != 1 || got["standard"] != 1 || got["document"] != 1 || got["vacancy"] != 0 {
		t.Fatalf("root coverage cells = %v", got)
	}
	if got := cells["n2"]; got["vacancy"] != 1 {
		t.Fatalf("function coverage cells = %v, want vacancy 1", got)
	}

	// Sources and template explanations (#2) cover every node.
	if len(payload.Sources) != 3 {
		t.Fatalf("sources = %+v, want 3", payload.Sources)
	}
	if payload.Sources[0].Priority != domain.PriorityInterview {
		t.Fatalf("interview priority = %d, want %d", payload.Sources[0].Priority, domain.PriorityInterview)
	}
	if len(payload.NodeExplanations) != 4 {
		t.Fatalf("explanations = %d, want one per node", len(payload.NodeExplanations))
	}
	rootExplanation := payload.NodeExplanations["n1"]
	if !strings.Contains(rootExplanation.Summary, "interview") || len(rootExplanation.Evidence) != 3 {
		t.Fatalf("root explanation = %+v", rootExplanation)
	}
	if rootExplanation.Evidence[0].Priority != domain.PriorityInterview {
		t.Fatalf("root evidence priority = %+v, want interview priority", rootExplanation.Evidence[0])
	}
}

// TestRunInsightsAuthBoundary pins the RBAC matrix: token required, student is
// forbidden, and every documented role reaches the store (501 for the
// in-memory store).
func TestRunInsightsAuthBoundary(t *testing.T) {
	handler := insightsTestHandler(NewMemStoreForTest())
	path := "/api/v1/runs/" + insightsTestRunID + "/insights"

	if response := do(t, handler, http.MethodGet, path, "", ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("no token: status = %d, want 401", response.Code)
	}
	if response := do(t, handler, http.MethodGet, path, "student", ""); response.Code != http.StatusForbidden {
		t.Fatalf("student: status = %d, want 403", response.Code)
	}
	for _, role := range []string{"admin", "teacher", "rop", "guest"} {
		if response := do(t, handler, http.MethodGet, path, role, ""); response.Code != http.StatusNotImplemented {
			t.Fatalf("%s: status = %d, want 501 (in-memory store cannot read run sources)", role, response.Code)
		}
	}
}

// TestRunCoverageAuthBoundary mirrors the RBAC checks for the coverage route.
func TestRunCoverageAuthBoundary(t *testing.T) {
	handler := insightsTestHandler(NewMemStoreForTest())
	path := "/api/v1/runs/" + insightsTestRunID + "/coverage"

	if response := do(t, handler, http.MethodGet, path, "", ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("no token: status = %d, want 401", response.Code)
	}
	if response := do(t, handler, http.MethodGet, path, "student", ""); response.Code != http.StatusForbidden {
		t.Fatalf("student: status = %d, want 403", response.Code)
	}
	if response := do(t, handler, http.MethodGet, path, "guest", ""); response.Code != http.StatusNotImplemented {
		t.Fatalf("guest: status = %d, want 501", response.Code)
	}
}

// TestRunInsightsErrors covers 404, 500 and empty-tree responses.
func TestRunInsightsErrors(t *testing.T) {
	t.Run("unknown run", func(t *testing.T) {
		fixture := insightsTestFixture()
		fixture.run = nil
		handler := insightsTestHandler(fixture)
		response := do(t, handler, http.MethodGet, "/api/v1/runs/"+insightsTestRunID+"/insights", "teacher", "")
		if response.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", response.Code)
		}
	})
	t.Run("tree error", func(t *testing.T) {
		fixture := insightsTestFixture()
		fixture.treeErr = errors.New("boom")
		handler := insightsTestHandler(fixture)
		response := do(t, handler, http.MethodGet, "/api/v1/runs/"+insightsTestRunID+"/insights", "teacher", "")
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", response.Code)
		}
	})
	t.Run("sources error", func(t *testing.T) {
		fixture := insightsTestFixture()
		fixture.sourcesErr = ErrServiceUnavailable
		handler := insightsTestHandler(fixture)
		response := do(t, handler, http.MethodGet, "/api/v1/runs/"+insightsTestRunID+"/insights", "teacher", "")
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", response.Code)
		}
	})
	t.Run("invalid run id", func(t *testing.T) {
		fixture := insightsTestFixture()
		handler := insightsTestHandler(fixture)
		response := do(t, handler, http.MethodGet, "/api/v1/runs/not-a-uuid/insights", "teacher", "")
		if response.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", response.Code)
		}
	})
}

// TestRunInsightsEmptyTree pins the empty-tree contract on the HTTP layer: the
// run exists but has no nodes yet, so all collections must be present and empty.
func TestRunInsightsEmptyTree(t *testing.T) {
	fixture := &insightsFakeStore{
		run:   &domain.Run{ID: insightsTestRunID, Status: domain.RunPending, Stage: domain.StageCreated},
		nodes: nil,
	}
	handler := insightsTestHandler(fixture)
	response := do(t, handler, http.MethodGet, "/api/v1/runs/"+insightsTestRunID+"/insights", "guest", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", response.Code, response.Body.String())
	}
	var payload analysis.Insights
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("insights JSON: %v", err)
	}
	if payload.Health.Score != 0 || payload.Health.Breakdown != (analysis.HealthBreakdown{}) {
		t.Fatalf("empty health = %+v, want zeroes", payload.Health)
	}
	if payload.Provenance.Nodes == nil || len(payload.Provenance.Nodes) != 0 {
		t.Fatalf("provenance nodes = %#v, want non-nil empty map", payload.Provenance.Nodes)
	}
	if payload.Flags == nil || len(payload.Flags) != 0 {
		t.Fatalf("flags = %#v, want non-nil empty array", payload.Flags)
	}
	if payload.MarketHeat.Source != "none" || payload.MarketHeat.Top == nil || len(payload.MarketHeat.Top) != 0 {
		t.Fatalf("market heat = %+v, want none/empty", payload.MarketHeat)
	}
	if payload.Coverage.Rows == nil || len(payload.Coverage.Rows) != 0 || len(payload.Coverage.Columns) != 4 {
		t.Fatalf("coverage = %+v, want four columns and no rows", payload.Coverage)
	}
	if payload.NodeExplanations == nil || len(payload.NodeExplanations) != 0 {
		t.Fatalf("explanations = %#v, want non-nil empty map", payload.NodeExplanations)
	}
	if payload.Sources == nil || len(payload.Sources) != 0 {
		t.Fatalf("sources = %#v, want non-nil empty array", payload.Sources)
	}
}

// TestRunCoverageJSON checks the default JSON coverage response.
func TestRunCoverageJSON(t *testing.T) {
	handler := insightsTestHandler(insightsTestFixture())
	response := do(t, handler, http.MethodGet, "/api/v1/runs/"+insightsTestRunID+"/coverage", "rop", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", contentType)
	}
	var payload coverageResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("coverage JSON: %v", err)
	}
	if payload.RunID != insightsTestRunID {
		t.Fatalf("run_id = %q, want %q", payload.RunID, insightsTestRunID)
	}
	if len(payload.Columns) != 4 || len(payload.Rows) != 3 {
		t.Fatalf("coverage = %+v, want four columns and three rows", payload)
	}
}

// TestRunCoverageCSV checks the text/csv contract, the case-insensitive format
// switch and the attachment filename.
func TestRunCoverageCSV(t *testing.T) {
	handler := insightsTestHandler(insightsTestFixture())
	path := "/api/v1/runs/" + insightsTestRunID + "/coverage?format=csv"
	response := do(t, handler, http.MethodGet, path, "teacher", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "text/csv; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want text/csv; charset=utf-8", contentType)
	}
	if disposition := response.Header().Get("Content-Disposition"); !strings.Contains(disposition, insightsTestRunID+"-coverage.csv") {
		t.Fatalf("Content-Disposition = %q, want the run coverage filename", disposition)
	}
	want := "node_id,title,type,interview,standard,vacancy,document\n" +
		"n1,Аналитик данных,role,1,1,0,1\n" +
		"n2,Отчётность и визуализация,function,0,0,1,0\n" +
		"n3,Писать SQL-запросы,task,1,0,0,0\n"
	if response.Body.String() != want {
		t.Fatalf("csv body =\n%q\nwant\n%q", response.Body.String(), want)
	}

	// Any other format value (or none) keeps the JSON default.
	for _, format := range []string{"", "json", "CSV ", "CSV"} {
		query := ""
		if format != "" {
			query = "?format=" + strings.ReplaceAll(format, " ", "%20")
		}
		probe := do(t, handler, http.MethodGet, "/api/v1/runs/"+insightsTestRunID+"/coverage"+query, "teacher", "")
		if format == "CSV" || format == "CSV " {
			if probe.Code != http.StatusOK || probe.Header().Get("Content-Type") != "text/csv; charset=utf-8" {
				t.Fatalf("format %q: status = %d, content type = %q, want CSV", format, probe.Code, probe.Header().Get("Content-Type"))
			}
			continue
		}
		if probe.Code != http.StatusOK || !strings.HasPrefix(probe.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("format %q: status = %d, content type = %q, want JSON", format, probe.Code, probe.Header().Get("Content-Type"))
		}
	}
}

// TestInsightFilenameIDSanitized guards the Content-Disposition helper against
// path/header injection from fixture ids.
func TestInsightFilenameIDSanitized(t *testing.T) {
	cases := map[string]string{
		"11111111-1111-1111-1111-111111111111": "11111111-1111-1111-1111-111111111111",
		"run 1/../x":                           "run1x",
		"":                                     "unknown",
		"../../etc/passwd":                     "etcpasswd",
	}
	for input, want := range cases {
		if got := insightFilenameID(input); got != want {
			t.Fatalf("insightFilenameID(%q) = %q, want %q", input, got, want)
		}
	}
}

// insightsIntegrationPool returns a pool for the optional PostgreSQL
// integration test. It is skipped unless a separate test database is configured
// (never run against the working database).
func insightsIntegrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("INGEST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("нужна отдельная тестовая PostgreSQL (INGEST_TEST_DATABASE_URL)")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestRunInsightsPostgresEndToEnd covers the real PGStore read path: a saved
// tree plus run sources are served by the registered handlers.
func TestRunInsightsPostgresEndToEnd(t *testing.T) {
	pool := insightsIntegrationPool(t)
	ctx := context.Background()
	store := NewPGStore(pool)
	handler := insightsTestHandler(store)

	run, err := store.CreateRun("Инсайты")
	if err != nil {
		t.Fatal(err)
	}
	createdSources := make([]string, 0, 2)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM runs WHERE id=$1::uuid`, run.ID)
		for _, sourceID := range createdSources {
			_, _ = pool.Exec(context.Background(), `DELETE FROM sources WHERE id=$1::uuid`, sourceID)
		}
	})

	interview, err := store.CreateSource(domain.Source{Type: domain.SourceInterview, Title: "Интервью", Content: "Пишу SQL-запросы"})
	if err != nil {
		t.Fatal(err)
	}
	createdSources = append(createdSources, interview.ID)
	vacancy, err := store.CreateSource(domain.Source{Type: domain.SourceVacancy, Title: "Вакансия", Content: "SQL, Python"})
	if err != nil {
		t.Fatal(err)
	}
	createdSources = append(createdSources, vacancy.ID)
	if err = store.LinkRunSources(ctx, run.ID, createdSources); err != nil {
		t.Fatal(err)
	}

	uuid := func() string {
		var id string
		if err = pool.QueryRow(ctx, "SELECT gen_random_uuid()::text").Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	rootID, functionID, taskID := uuid(), uuid(), uuid()
	nodes := []domain.Node{
		{ID: rootID, Type: domain.NodeRole, Title: "Аналитик данных", Evidence: []domain.Evidence{
			{ID: uuid(), SourceID: interview.ID, Quote: "Пишу SQL-запросы"},
			{ID: uuid(), SourceID: vacancy.ID, Quote: "SQL, Python"},
		}},
		{ID: functionID, Type: domain.NodeFunction, Title: "Отчётность", ParentID: &rootID, Evidence: []domain.Evidence{
			{ID: uuid(), SourceID: interview.ID, Quote: "Готовлю отчёты"},
		}},
		{ID: taskID, Type: domain.NodeTask, Title: "Писать SQL-запросы", ParentID: &functionID, Evidence: []domain.Evidence{
			{ID: uuid(), SourceID: interview.ID, Quote: "SQL-запросы каждый день"},
		}},
	}
	if err = store.SaveTree(ctx, run.ID, nodes); err != nil {
		t.Fatal(err)
	}

	response := do(t, handler, http.MethodGet, "/api/v1/runs/"+run.ID+"/insights", "teacher", "")
	if response.Code != http.StatusOK {
		t.Fatalf("insights status = %d (%s)", response.Code, response.Body.String())
	}
	var payload analysis.Insights
	if err = json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("insights JSON: %v", err)
	}
	if payload.RunID != run.ID {
		t.Fatalf("run_id = %q, want %q", payload.RunID, run.ID)
	}
	if got := payload.Provenance.Nodes[rootID]; got.SourceCount != 2 || got.EvidenceCount != 2 {
		t.Fatalf("root provenance = %+v, want 2 sources / 2 evidence", got)
	}
	if len(payload.Sources) != 2 {
		t.Fatalf("sources = %+v, want the two linked run sources", payload.Sources)
	}
	// PGStore.GetRun does not persist SkillFreq, so the deterministic
	// vacancy-content fallback is used: the vacancy source content "SQL, Python"
	// is the only market document.
	if payload.MarketHeat.Source != "vacancy_sources" || payload.MarketHeat.SampleSize != 1 {
		t.Fatalf("market heat = %+v, want vacancy_sources with sample_size 1", payload.MarketHeat)
	}
	if len(payload.MarketHeat.Top) == 0 {
		t.Fatal("market heat top is empty, want skills extracted from the vacancy source")
	}
	if got := payload.MarketHeat.Nodes[taskID]; got.Score != 1 || len(got.Matches) != 1 || got.Matches[0] != "sql" {
		t.Fatalf("task market heat = %+v, want sql/1 from the vacancy content", got)
	}

	csvResponse := do(t, handler, http.MethodGet, "/api/v1/runs/"+run.ID+"/coverage?format=csv", "teacher", "")
	if csvResponse.Code != http.StatusOK {
		t.Fatalf("coverage csv status = %d (%s)", csvResponse.Code, csvResponse.Body.String())
	}
	lines := strings.Split(strings.TrimSpace(csvResponse.Body.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("csv lines = %d, want header + 3 rows: %q", len(lines), csvResponse.Body.String())
	}
	if lines[0] != "node_id,title,type,interview,standard,vacancy,document" {
		t.Fatalf("csv header = %q", lines[0])
	}
	if !strings.Contains(csvResponse.Body.String(), fmt.Sprintf("%s,Аналитик данных,role,1,0,1,0", rootID)) {
		t.Fatalf("csv body = %q, want root row with interview+vacancy", csvResponse.Body.String())
	}
}

// TestRunInsightsHandlersRejectUninitializedPGStore covers the missing-PG-store
// branch: a PGStore with a nil pool must answer 501 instead of panicking.
func TestRunInsightsHandlersRejectUninitializedPGStore(t *testing.T) {
	empty := &insightsFakeStore{}
	server := NewServer(empty)
	server.store = &PGStore{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/runs/{id}/insights", server.handleRunInsights)
	mux.HandleFunc("GET /api/v1/runs/{id}/coverage", server.handleRunCoverage)
	handler := auth.Middleware(mux)

	for _, path := range []string{"/insights", "/coverage"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/runs/"+insightsTestRunID+path, nil)
		req.Header.Set("Authorization", "Bearer "+auth.TokenFor("test-user", auth.RoleAdmin))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusNotImplemented {
			t.Fatalf("%s: status = %d, want 501", path, recorder.Code)
		}
	}
}

// TestRunInsightsMarketHeatFallback covers variant (c): without Run.SkillFreq
// the handler deterministically extracts market frequencies from the content of
// vacancy/standard run sources and reports source "vacancy_sources" plus
// sample_size.
func TestRunInsightsMarketHeatFallback(t *testing.T) {
	fixture := insightsTestMarketFixture()
	handler := insightsTestHandler(fixture)
	response := do(t, handler, http.MethodGet, "/api/v1/runs/"+insightsTestRunID+"/insights", "teacher", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", response.Code, response.Body.String())
	}
	var payload analysis.Insights
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("insights JSON: %v", err)
	}
	heat := payload.MarketHeat
	if heat.Source != "vacancy_sources" || heat.SampleSize != 2 {
		t.Fatalf("market heat = source %q sample %d, want vacancy_sources/2 (interview ignored)", heat.Source, heat.SampleSize)
	}
	if fixture.contentsCalls != 1 {
		t.Fatalf("source contents calls = %d, want 1 fallback read", fixture.contentsCalls)
	}
	if len(heat.Top) == 0 || heat.Top[0].Skill != "sql" || heat.Top[0].Frequency != 2 {
		t.Fatalf("market heat top = %+v, want sql/2 first (SQL appears in both documents)", heat.Top)
	}
	if got := heat.Nodes["n3"]; got.Score != 2 || len(got.Matches) != 1 || got.Matches[0] != "sql" {
		t.Fatalf("task market heat = %+v, want sql/2", got)
	}
	if got := heat.Nodes["n2"]; got.Score != 2 || len(got.Matches) != 2 {
		t.Fatalf("function market heat = %+v, want two matched skills totaling 2", got)
	}
	if _, ok := heat.Nodes["n1"]; ok {
		t.Fatalf("role must not have market heat: %+v", heat.Nodes["n1"])
	}
}

// TestRunInsightsMarketHeatSkillFreqPrecedence proves Run.SkillFreq wins over
// the source-content fallback and that contents are not even read.
func TestRunInsightsMarketHeatSkillFreqPrecedence(t *testing.T) {
	fixture := insightsTestFixture() // SkillFreq SQL=42 and contents Go/Kubernetes
	handler := insightsTestHandler(fixture)
	response := do(t, handler, http.MethodGet, "/api/v1/runs/"+insightsTestRunID+"/insights", "teacher", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", response.Code, response.Body.String())
	}
	var payload analysis.Insights
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("insights JSON: %v", err)
	}
	if payload.MarketHeat.Source != "run.skill_freq" {
		t.Fatalf("market heat source = %q, want run.skill_freq", payload.MarketHeat.Source)
	}
	if payload.MarketHeat.SampleSize != 0 {
		t.Fatalf("market heat sample_size = %d, want 0 for run.skill_freq", payload.MarketHeat.SampleSize)
	}
	if len(payload.MarketHeat.Top) != 1 || payload.MarketHeat.Top[0].Skill != "SQL" || payload.MarketHeat.Top[0].Frequency != 42 {
		t.Fatalf("market heat top = %+v, want SQL/42", payload.MarketHeat.Top)
	}
	if fixture.contentsCalls != 0 {
		t.Fatalf("source contents calls = %d, want 0 (precedence)", fixture.contentsCalls)
	}
}

// TestRunInsightsMarketHeatNoData proves empty market data stays a successful
// empty overlay: no skill frequency and no usable market sources.
func TestRunInsightsMarketHeatNoData(t *testing.T) {
	cases := []struct {
		name     string
		contents []domain.Source
	}{
		{"nil contents", nil},
		{"interview-only contents", []domain.Source{
			{ID: insightsTestInterviewID, Type: domain.SourceInterview, Title: "Interview transcript", Content: "SQL, Python"},
		}},
		{"empty content", []domain.Source{
			{ID: insightsTestVacancyID, Type: domain.SourceVacancy, Title: "Вакансия", Content: "   "},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := insightsTestMarketFixture()
			fixture.contents = tc.contents
			handler := insightsTestHandler(fixture)
			response := do(t, handler, http.MethodGet, "/api/v1/runs/"+insightsTestRunID+"/insights", "guest", "")
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d (%s), want 200", response.Code, response.Body.String())
			}
			var payload analysis.Insights
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatalf("insights JSON: %v", err)
			}
			heat := payload.MarketHeat
			if heat.Source != "none" || heat.SampleSize != 0 || len(heat.Top) != 0 || len(heat.Nodes) != 0 {
				t.Fatalf("market heat = %+v, want empty none overlay", heat)
			}
		})
	}
}

// TestRunInsightsMarketHeatFallbackErrorIgnored keeps the fallback best-effort:
// a source-content read error must not fail the whole insights request.
func TestRunInsightsMarketHeatFallbackErrorIgnored(t *testing.T) {
	fixture := insightsTestMarketFixture()
	fixture.contentsErr = errors.New("source storage unavailable")
	handler := insightsTestHandler(fixture)
	response := do(t, handler, http.MethodGet, "/api/v1/runs/"+insightsTestRunID+"/insights", "teacher", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200 even when fallback source content fails", response.Code, response.Body.String())
	}
	var payload analysis.Insights
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("insights JSON: %v", err)
	}
	if payload.MarketHeat.Source != "none" {
		t.Fatalf("market heat source = %q, want none when the fallback read fails", payload.MarketHeat.Source)
	}
}

// TestRunInsightsMarketHeatFallbackTopTen bounds the fallback top list to 10
// skills, ordered by frequency desc then name asc.
func TestRunInsightsMarketHeatFallbackTopTen(t *testing.T) {
	fixture := insightsTestMarketFixture()
	fixture.contents = []domain.Source{
		{ID: insightsTestVacancyID, Type: domain.SourceVacancy, Title: "Вакансия",
			Content: "SQL Python PostgreSQL Java Go Docker Kubernetes Terraform Linux Bash Git React TypeScript"},
	}
	handler := insightsTestHandler(fixture)
	response := do(t, handler, http.MethodGet, "/api/v1/runs/"+insightsTestRunID+"/insights", "teacher", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", response.Code, response.Body.String())
	}
	var payload analysis.Insights
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("insights JSON: %v", err)
	}
	if payload.MarketHeat.SampleSize != 1 {
		t.Fatalf("sample_size = %d, want 1", payload.MarketHeat.SampleSize)
	}
	if len(payload.MarketHeat.Top) != 10 {
		t.Fatalf("top length = %d, want 10", len(payload.MarketHeat.Top))
	}
	if payload.MarketHeat.Top[0].Skill != "bash" || payload.MarketHeat.Top[9].Skill != "react" {
		t.Fatalf("top = %+v, want alphabetical order for tied frequencies", payload.MarketHeat.Top)
	}
}
