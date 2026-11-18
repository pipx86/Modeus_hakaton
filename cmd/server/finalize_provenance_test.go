package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/agent/synthesizer"
	"activity-descriptor/internal/api"
	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"

	"github.com/jackc/pgx/v5/pgtype"
)

const (
	testStandardSourceID = "11111111-1111-4111-8111-111111111111"
	testVacancySourceID  = "22222222-2222-4222-8222-222222222222"
)

// provenanceStore is fakeFinalizeStore plus the two optional source capabilities
// the production PGStore implements: full run-source content and linking of
// fallback sources.
type provenanceStore struct {
	*fakeFinalizeStore
	contents    []domain.Source
	contentsErr error
	linkedRunID string
	linkedIDs   []string
	linkErr     error
}

func (s *provenanceStore) ListRunSourceContents(_ context.Context, _ string) ([]domain.Source, error) {
	return s.contents, s.contentsErr
}

func (s *provenanceStore) LinkRunSources(_ context.Context, runID string, sourceIDs []string) error {
	s.linkedRunID = runID
	s.linkedIDs = append(s.linkedIDs, sourceIDs...)
	return s.linkErr
}

func provenanceRunSources() ([]domain.Source, []domain.Source) {
	metadata := []domain.Source{
		{ID: testStandardSourceID, Type: domain.SourceStandard, Title: "Профстандарт", Priority: domain.PriorityStandard},
		{ID: testVacancySourceID, Type: domain.SourceVacancy, Title: "Вакансия", Priority: domain.PriorityVacancy},
	}
	withContent := []domain.Source{
		{ID: testStandardSourceID, Type: domain.SourceStandard, Title: "Профстандарт", Content: "Профстандарт: роль Аналитик данных. Задача: писать SQL-запросы.", Priority: domain.PriorityStandard},
		{ID: testVacancySourceID, Type: domain.SourceVacancy, Title: "Вакансия", Content: "Вакансия Data Analyst: строить дашборды.", Priority: domain.PriorityVacancy},
	}
	return metadata, withContent
}

func conflictingProvenanceEntities() []agent.ExtractedEntity {
	return []agent.ExtractedEntity{
		{Node: domain.Node{ID: "role-1", Type: domain.NodeRole, Title: "Аналитик данных",
			Evidence: []domain.Evidence{{ID: "ev-std", SourceID: testStandardSourceID, RefID: "req-1", Quote: "роль Аналитик данных"}}}},
		{Node: domain.Node{ID: "role-1", Type: domain.NodeRole, Title: "Data Analyst",
			Evidence: []domain.Evidence{{ID: "ev-vac", SourceID: testVacancySourceID, RefID: "line-1", Quote: "Вакансия Data Analyst"}}}},
	}
}

// TestFinalizeInterviewFeedsRunSourceContentsAndPersistsConflict is the unit
// level reproduction of the live goal: the analyst receives the content of the
// standard/vacancy sources linked to the run, its entities are routed to the
// Standard/Vacancy synthesizer slots, the deterministic synthesizer resolves the
// conflicting titles and the resolved conflict is persisted.
func TestFinalizeInterviewFeedsRunSourceContentsAndPersistsConflict(t *testing.T) {
	metadata, contents := provenanceRunSources()
	store := &provenanceStore{
		fakeFinalizeStore: &fakeFinalizeStore{sources: metadata},
		contents:          contents,
	}
	analyst := &fakeInterviewAnalyst{output: agent.AnalystOutput{Entities: conflictingProvenanceEntities()}}
	synth, err := synthesizer.New()
	if err != nil {
		t.Fatalf("synthesizer.New: %v", err)
	}

	status, err := finalizeInterview(context.Background(), finalizerDeps{
		store:       store,
		analyst:     analyst,
		synthesizer: synth,
	}, "run-provenance", "Аналитик данных", testFinalizeSession())
	if err != nil || status != domain.RunDone {
		t.Fatalf("finalize = %v, %v; want done", status, err)
	}

	// The analyst was fed the run-linked contents, not just metadata.
	if len(analyst.input.Sources) != len(contents) {
		t.Fatalf("analyst sources = %+v, want run sources with content", analyst.input.Sources)
	}
	for i := range contents {
		if analyst.input.Sources[i].ID != contents[i].ID || analyst.input.Sources[i].Content == "" {
			t.Fatalf("analyst source %d = %+v, want content", i, analyst.input.Sources[i])
		}
	}

	// One resolved title conflict between the standard and the vacancy source.
	if len(store.reviewConflicts) != 1 {
		t.Fatalf("persisted conflicts = %+v, want exactly one title conflict", store.reviewConflicts)
	}
	conflict := store.reviewConflicts[0]
	if conflict.NodeID != "role-1" || conflict.Field != "title" || conflict.Winner.SourceType != domain.SourceStandard {
		t.Fatalf("conflict = %+v, want standard winning the shared title", conflict)
	}
	if len(store.reviewMessages) != 1 || store.reviewMessages[0].Kind != "conflict" {
		t.Fatalf("review messages = %+v, want one conflict message", store.reviewMessages)
	}

	// The persisted tree references both real source ids instead of rewriting
	// all evidence to the interview transcript.
	if len(store.savedTree) != 1 {
		t.Fatalf("saved tree = %+v, want merged role", store.savedTree)
	}
	sourceIDs := make(map[string]bool)
	for _, evidence := range store.savedTree[0].Evidence {
		sourceIDs[evidence.SourceID] = true
	}
	if !sourceIDs[testStandardSourceID] || !sourceIDs[testVacancySourceID] {
		t.Fatalf("saved evidence sources = %+v, want standard and vacancy source ids", sourceIDs)
	}
}

// TestFinalizeInterviewPartitionsGlobalSourcesIntoVacancy verifies that sources
// which were not linked to the run (for example admin/global material retrieved
// by the finalizer) still reach the synthesizer with their type instead of
// falling back to Interview.
func TestFinalizeInterviewPartitionsGlobalSourcesIntoVacancy(t *testing.T) {
	global := []domain.Source{{ID: "global-vacancy-1", Type: domain.SourceVacancy, Title: "Глобальная вакансия", Content: "Вакансия Data Analyst: строить дашборды."}}
	store := &provenanceStore{fakeFinalizeStore: &fakeFinalizeStore{}}
	retriever := sourceRetriever(func(context.Context, string, string) ([]domain.Source, error) {
		return global, nil
	})
	entity := agent.ExtractedEntity{Node: domain.Node{ID: "fn-1", Type: domain.NodeFunction, Title: "Дашборды",
		Evidence: []domain.Evidence{{ID: "ev-1", SourceID: "global-vacancy-1", RefID: "line-1", Quote: "строить дашборды"}}}}
	synth := &fakeSynthesisRunner{output: agent.SynthesizerOutput{Tree: testInterviewNodes()}}

	status, err := finalizeInterview(context.Background(), finalizerDeps{
		store:       store,
		analyst:     &fakeInterviewAnalyst{output: agent.AnalystOutput{Entities: []agent.ExtractedEntity{entity}}},
		synthesizer: synth,
		retrievers:  []sourceRetriever{retriever},
	}, "run-global", "Аналитик данных", testFinalizeSession())
	if err != nil || status != domain.RunDone {
		t.Fatalf("finalize = %v, %v; want done", status, err)
	}
	if len(synth.input.Vacancy) != 1 || synth.input.Vacancy[0].ID != "fn-1" {
		t.Fatalf("vacancy nodes = %+v, want the global vacancy entity", synth.input.Vacancy)
	}
	if len(synth.input.Standard) != 0 || len(synth.input.Interview) != 0 {
		t.Fatalf("unexpected slots: standard=%+v interview=%+v", synth.input.Standard, synth.input.Interview)
	}
	// The fallback source is attached to the run so provenance survives.
	if !reflect.DeepEqual(store.linkedIDs, []string{"global-vacancy-1"}) {
		t.Fatalf("linked source ids = %+v", store.linkedIDs)
	}
}

func TestAssignUUIDsPreservingSourcesKeepsRealSourceIDs(t *testing.T) {
	rootID := "role-1"
	nodes := []domain.Node{{ID: rootID, Type: domain.NodeRole, Title: "Аналитик",
		Evidence: []domain.Evidence{
			{ID: "e1", SourceID: testStandardSourceID, RefID: "r1", Quote: "q1"},
			{ID: "e2", SourceID: "src-interview", RefID: "r2", Quote: "q2"},
		}}}
	interviewSourceID := newUUID()
	out := assignUUIDsPreservingSources(nodes, interviewSourceID, map[string]bool{testStandardSourceID: true})
	if len(out) != 1 || len(out[0].Evidence) != 2 {
		t.Fatalf("out = %+v", out)
	}
	if out[0].Evidence[0].SourceID != testStandardSourceID {
		t.Fatalf("real source id was rewritten: %+v", out[0].Evidence[0])
	}
	if out[0].Evidence[1].SourceID != interviewSourceID.String() {
		t.Fatalf("unknown source id = %q, want interview source", out[0].Evidence[1].SourceID)
	}
}

// TestFinalizeInterviewPostgresSourceEvidenceEndToEnd runs the real production
// path against PostgreSQL (no LLM): run-linked standard/vacancy sources reach the
// analyst, the persisted tree keeps both real source ids, a cross-source conflict
// is stored and served by GET /conflicts.
func TestFinalizeInterviewPostgresSourceEvidenceEndToEnd(t *testing.T) {
	pool := finalizeTestPool(t)
	ctx := context.Background()
	store := api.NewPGStore(pool)

	run, err := store.CreateRun("Аналитик данных")
	if err != nil {
		t.Fatal(err)
	}
	var standardID, vacancyID string
	if err = pool.QueryRow(ctx, `INSERT INTO sources(type,title,content,run_id) VALUES('standard','Профстандарт','роль Аналитик данных, писать SQL-запросы',$1::uuid) RETURNING id::text`, run.ID).Scan(&standardID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO sources(type,title,content,run_id) VALUES('vacancy','Вакансия','Вакансия Data Analyst, строить дашборды',$1::uuid) RETURNING id::text`, run.ID).Scan(&vacancyID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM runs WHERE id=$1::uuid`, run.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM sources WHERE id=$1::uuid`, standardID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM sources WHERE id=$1::uuid`, vacancyID)
	})

	entities := []agent.ExtractedEntity{
		{Node: domain.Node{ID: "role-1", Type: domain.NodeRole, Title: "Аналитик данных",
			Evidence: []domain.Evidence{{ID: "ev-std", SourceID: standardID, RefID: "req-1", Quote: "роль Аналитик данных"}}}},
		{Node: domain.Node{ID: "role-1", Type: domain.NodeRole, Title: "Data Analyst",
			Evidence: []domain.Evidence{{ID: "ev-vac", SourceID: vacancyID, RefID: "line-1", Quote: "Вакансия Data Analyst"}}}},
	}
	synth, err := synthesizer.New()
	if err != nil {
		t.Fatalf("synthesizer.New: %v", err)
	}
	status, err := finalizeInterview(ctx, finalizerDeps{
		store:       store,
		analyst:     &fakeInterviewAnalyst{output: agent.AnalystOutput{Entities: entities}},
		synthesizer: synth,
	}, run.ID, "Аналитик данных", testFinalizeSession())
	if err != nil || status != domain.RunDone {
		t.Fatalf("finalize = %v, %v; want done", status, err)
	}

	// Both material sources and the created interview source are linked.
	var linked int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM run_sources WHERE run_id=$1::uuid`, run.ID).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if linked < 3 {
		t.Fatalf("run_sources = %d, want standard + vacancy + interview", linked)
	}
	// The merged tree references both real material source ids.
	tree, err := store.GetTree(run.ID)
	if err != nil || len(tree) == 0 {
		t.Fatalf("tree = %+v, %v", tree, err)
	}
	sourceIDs := make(map[string]bool)
	for _, node := range tree {
		for _, evidence := range node.Evidence {
			sourceIDs[evidence.SourceID] = true
		}
	}
	if !sourceIDs[standardID] || !sourceIDs[vacancyID] {
		t.Fatalf("tree evidence sources = %+v, want both material ids", sourceIDs)
	}

	// Authenticated API serves the persisted conflict.
	handler := api.NewServer(store).Routes()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/runs/"+run.ID+"/conflicts", nil)
	request.Header.Set("Authorization", "Bearer "+auth.TokenFor("reviewer", auth.RoleTeacher))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET conflicts = %d %s", recorder.Code, recorder.Body.String())
	}
	var conflicts []domain.Conflict
	if err = json.Unmarshal(recorder.Body.Bytes(), &conflicts); err != nil {
		t.Fatalf("conflicts JSON: %v", err)
	}
	if len(conflicts) < 1 {
		t.Fatalf("conflicts = %+v, want >= 1 persisted conflict", conflicts)
	}
	if conflicts[0].NodeID == "" || len(conflicts[0].Values) < 2 {
		t.Fatalf("conflict = %+v, want both competing source values", conflicts[0])
	}
	sourceTypes := make(map[domain.SourceType]bool)
	for _, value := range conflicts[0].Values {
		sourceTypes[value.SourceType] = true
	}
	if !sourceTypes[domain.SourceStandard] || !sourceTypes[domain.SourceVacancy] {
		t.Fatalf("conflict values = %+v, want standard and vacancy", conflicts[0].Values)
	}
	if !strings.Contains(conflicts[0].Winner.SourceType.String(), "standard") {
		t.Fatalf("winner = %+v, want standard", conflicts[0].Winner)
	}
}

func TestKnownSourceIDsIgnoresSyntheticIDs(t *testing.T) {
	ids := knownSourceIDs([]domain.Source{
		{ID: testStandardSourceID},
		{ID: strings.ToUpper(testVacancySourceID)},
		{ID: "src-interview"},
		{ID: ""},
	})
	if !ids[testStandardSourceID] || !ids[strings.ToLower(testVacancySourceID)] {
		t.Fatalf("known ids = %+v", ids)
	}
	if ids["src-interview"] {
		t.Fatal("synthetic source id treated as a database row")
	}
}

var _ = pgtype.UUID{}
