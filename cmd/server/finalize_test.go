package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"activity-descriptor/internal/agent"
	"activity-descriptor/internal/agent/synthesizer"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestTranscriptFromSession(t *testing.T) {
	session := domain.InterviewSession{
		Questions: []domain.Question{{Text: "Что вы делаете?"}, {Text: "Какие задачи?"}},
		Answers:   []domain.Answer{{Text: "Анализирую данные."}, {Text: "Строю отчёты."}},
	}
	got := transcriptFromSession(session)
	want := "Вопрос: Что вы делаете?\nОтвет: Анализирую данные.\nВопрос: Какие задачи?\nОтвет: Строю отчёты.\n"
	if got != want {
		t.Fatalf("transcript = %q, want %q", got, want)
	}
}

func TestAssignUUIDsPreservesHierarchyAndEvidence(t *testing.T) {
	parent := "role-1"
	nodes := []domain.Node{
		{ID: parent, Type: domain.NodeRole, Title: "Аналитик", Evidence: []domain.Evidence{{ID: "e1", SourceID: "src", Quote: "q1"}}},
		{ID: "task-1", Type: domain.NodeTask, Title: "Отчёты", ParentID: &parent, Evidence: []domain.Evidence{{ID: "e2", SourceID: "src", Quote: "q2"}}},
	}
	sourceID := newUUID()
	out := assignUUIDs(nodes, sourceID)
	if len(out) != 2 {
		t.Fatalf("len = %d", len(out))
	}
	if out[0].ID == parent || out[1].ID == "task-1" {
		t.Fatal("node IDs were not replaced with UUIDs")
	}
	if out[1].ParentID == nil || *out[1].ParentID != out[0].ID {
		t.Fatalf("parent was not remapped: %+v", out[1].ParentID)
	}
	for _, node := range out {
		for _, evidence := range node.Evidence {
			if evidence.SourceID != sourceID.String() {
				t.Fatalf("source id = %q, want %q", evidence.SourceID, sourceID.String())
			}
			if evidence.ID == "" || evidence.ID == "e1" || evidence.ID == "e2" {
				t.Fatalf("evidence ID was not replaced: %q", evidence.ID)
			}
		}
	}
}

// ---- fake finalizer collaborators ----

type fakeFinalizeStore struct {
	sources    []domain.Source
	sourcesErr error

	stages []domain.Stage

	interviewRunID      string
	interviewSourceID   string
	interviewTranscript string
	createSourceErr     error

	savedTree   []domain.Node
	saveTreeErr error

	snapshotCalls int
	snapshotErr   error

	reviewRunID     string
	reviewConflicts []domain.Conflict
	reviewMessages  []agent.AgentMessage
	reviewErr       error
}

func (f *fakeFinalizeStore) SetStage(_ string, stage domain.Stage) error {
	f.stages = append(f.stages, stage)
	return nil
}

func (f *fakeFinalizeStore) ListRunSources(_ context.Context, _ string) ([]domain.Source, error) {
	return f.sources, f.sourcesErr
}

func (f *fakeFinalizeStore) CreateInterviewSource(_ context.Context, runID, sourceID, transcript string) error {
	f.interviewRunID, f.interviewSourceID, f.interviewTranscript = runID, sourceID, transcript
	return f.createSourceErr
}

func (f *fakeFinalizeStore) SaveTree(_ context.Context, _ string, nodes []domain.Node) error {
	f.savedTree = nodes
	return f.saveTreeErr
}

func (f *fakeFinalizeStore) SaveRunSnapshot(_ context.Context, _ string) error {
	f.snapshotCalls++
	return f.snapshotErr
}

func (f *fakeFinalizeStore) SaveSynthesisReview(_ context.Context, runID string, conflicts []domain.Conflict, messages []agent.AgentMessage) error {
	f.reviewRunID, f.reviewConflicts, f.reviewMessages = runID, conflicts, messages
	return f.reviewErr
}

type fakeInterviewAnalyst struct {
	called bool
	input  agent.AnalystInput
	output agent.AnalystOutput
	err    error
}

func (f *fakeInterviewAnalyst) Analyze(_ context.Context, in agent.AnalystInput) (agent.AnalystOutput, error) {
	f.called = true
	f.input = in
	return f.output, f.err
}

type fakeSynthesisRunner struct {
	called bool
	input  agent.SynthesizerInput
	output agent.SynthesizerOutput
	err    error
}

func (f *fakeSynthesisRunner) Synthesize(_ context.Context, in agent.SynthesizerInput) (agent.SynthesizerOutput, error) {
	f.called = true
	f.input = in
	return f.output, f.err
}

type fakeChatClient struct {
	responses []string
	calls     int
}

func (f *fakeChatClient) Chat(_ context.Context, _ []llm.Message) (string, error) {
	if f.calls >= len(f.responses) {
		return "", errors.New("unexpected llm call")
	}
	response := f.responses[f.calls]
	f.calls++
	return response, nil
}

// ---- shared fixtures ----

const testFinalizeTranscript = "Вопрос: Кто вы?\nОтвет: Я аналитик данных.\nВопрос: Что делаете?\nОтвет: Строю отчёты.\n"

func testFinalizeSession() domain.InterviewSession {
	return domain.InterviewSession{
		ID:        "session-1",
		Questions: []domain.Question{{ID: "q1", Text: "Кто вы?"}, {ID: "q2", Text: "Что делаете?"}},
		Answers:   []domain.Answer{{QuestionID: "q1", Text: "Я аналитик данных."}, {QuestionID: "q2", Text: "Строю отчёты."}},
	}
}

func testInterviewNodes() []domain.Node {
	rootID := "role-1"
	return []domain.Node{
		{ID: rootID, Type: domain.NodeRole, Title: "Аналитик данных",
			Evidence: []domain.Evidence{{ID: "ev-1", SourceID: "src-interview", RefID: "q1", Quote: "Я аналитик данных."}}},
		{ID: "fn-1", Type: domain.NodeFunction, Title: "Отчётность", ParentID: &rootID,
			Evidence: []domain.Evidence{{ID: "ev-2", SourceID: "src-interview", RefID: "q2", Quote: "Строю отчёты."}}},
	}
}

func testAnalystOutput() agent.AnalystOutput {
	nodes := testInterviewNodes()
	entities := make([]agent.ExtractedEntity, 0, len(nodes))
	for _, node := range nodes {
		entities = append(entities, agent.ExtractedEntity{Node: node})
	}
	return agent.AnalystOutput{Entities: entities}
}

func testConflict() domain.Conflict {
	return domain.Conflict{
		ID:     "c-1",
		NodeID: "role-1",
		Field:  "title",
		Values: []domain.ConflictValue{
			{SourceID: "src-interview", SourceType: domain.SourceInterview, Value: "Аналитик данных", Priority: domain.PriorityInterview},
			{SourceID: "src-vacancy", SourceType: domain.SourceVacancy, Value: "Data Analyst", Priority: domain.PriorityVacancy},
		},
		Winner: domain.ConflictValue{SourceID: "src-interview", SourceType: domain.SourceInterview, Value: "Аналитик данных", Priority: domain.PriorityInterview},
		Reason: "higher source priority wins: interview (300)",
	}
}

func testSynthesisOutput() agent.SynthesizerOutput {
	conflict := testConflict()
	return agent.SynthesizerOutput{
		Tree:      testInterviewNodes(),
		Conflicts: []domain.Conflict{conflict},
		Messages:  synthesizer.ConflictMessages([]domain.Conflict{conflict}),
	}
}

func mustUUID(t *testing.T, value string) {
	t.Helper()
	var id pgtype.UUID
	if err := id.Scan(value); err != nil || !id.Valid {
		t.Fatalf("%q is not a UUID: %v", value, err)
	}
}

// ---- finalizer tests ----

func TestFinalizeInterviewUsesRunSourcesAndPersistsReview(t *testing.T) {
	sources := []domain.Source{
		{ID: "src-vacancy-1", Type: domain.SourceVacancy, Title: "Вакансия", Priority: domain.PriorityVacancy},
		{ID: "src-standard-1", Type: domain.SourceStandard, Title: "Стандарт", Priority: domain.PriorityStandard},
	}
	store := &fakeFinalizeStore{sources: sources}
	analyst := &fakeInterviewAnalyst{output: testAnalystOutput()}
	synthesis := testSynthesisOutput()
	synth := &fakeSynthesisRunner{output: synthesis}
	rag := []domain.Source{{ID: "src-rag-1", Type: domain.SourceDocument, Title: "RAG-фрагмент", Content: "цитата"}}
	retriever := sourceRetriever(func(context.Context, string, string) ([]domain.Source, error) { return rag, nil })

	status, err := finalizeInterview(context.Background(), finalizerDeps{
		store:       store,
		analyst:     analyst,
		synthesizer: synth,
		retrievers:  []sourceRetriever{retriever},
	}, "run-1", "Аналитик данных", testFinalizeSession())
	if err != nil || status != domain.RunDone {
		t.Fatalf("finalize = %v, %v; want done", status, err)
	}

	// Analyst по-прежнему получает RAG-контекст интервью.
	if !analyst.called || analyst.input.Transcript != testFinalizeTranscript || analyst.input.SessionID != "session-1" || analyst.input.Profession != "Аналитик данных" {
		t.Fatalf("analyst input = %+v", analyst.input)
	}
	if !reflect.DeepEqual(analyst.input.Sources, rag) {
		t.Fatalf("analyst sources = %+v, want %+v", analyst.input.Sources, rag)
	}

	// Synthesizer получает реальные источники run (метаданные) и interview-узлы;
	// несуществующие standard/vacancy-узлы не выдумываются.
	if !synth.called {
		t.Fatal("synthesizer was not called")
	}
	if synth.input.Profession != "Аналитик данных" {
		t.Fatalf("synthesizer profession = %q", synth.input.Profession)
	}
	if !reflect.DeepEqual(synth.input.Sources, sources) {
		t.Fatalf("synthesizer sources = %+v, want run sources %+v", synth.input.Sources, sources)
	}
	if len(synth.input.Interview) != len(testInterviewNodes()) {
		t.Fatalf("synthesizer interview nodes = %d", len(synth.input.Interview))
	}
	if len(synth.input.Vacancy) != 0 || len(synth.input.Standard) != 0 {
		t.Fatalf("finalizer fabricated standard/vacancy nodes: %+v / %+v", synth.input.Standard, synth.input.Vacancy)
	}

	// Конфликты и сообщения синтеза сохранены с run_id.
	if store.reviewRunID != "run-1" {
		t.Fatalf("review run id = %q", store.reviewRunID)
	}
	if !reflect.DeepEqual(store.reviewConflicts, synthesis.Conflicts) {
		t.Fatalf("persisted conflicts = %+v, want %+v", store.reviewConflicts, synthesis.Conflicts)
	}
	if !reflect.DeepEqual(store.reviewMessages, synthesis.Messages) {
		t.Fatalf("persisted messages = %+v, want %+v", store.reviewMessages, synthesis.Messages)
	}

	// Interview-источник создан, дерево сохранено с его UUID в evidence.
	if store.interviewRunID != "run-1" || store.interviewTranscript != testFinalizeTranscript {
		t.Fatalf("interview source run=%q transcript=%q", store.interviewRunID, store.interviewTranscript)
	}
	mustUUID(t, store.interviewSourceID)
	if len(store.savedTree) != 2 {
		t.Fatalf("saved tree = %d nodes", len(store.savedTree))
	}
	for _, node := range store.savedTree {
		mustUUID(t, node.ID)
		if len(node.Evidence) == 0 {
			t.Fatalf("saved node %q has no evidence", node.Title)
		}
		for _, evidence := range node.Evidence {
			if evidence.SourceID != store.interviewSourceID {
				t.Fatalf("evidence source = %q, want interview source %q", evidence.SourceID, store.interviewSourceID)
			}
		}
	}
	if store.snapshotCalls != 1 {
		t.Fatalf("snapshot calls = %d, want 1", store.snapshotCalls)
	}
	wantStages := []domain.Stage{domain.StageAnalysis, domain.StageSynthesis, domain.StageReview, domain.StageDone}
	if !reflect.DeepEqual(store.stages, wantStages) {
		t.Fatalf("stages = %+v, want %+v", store.stages, wantStages)
	}
}

func TestFinalizeInterviewWithoutRunSourcesKeepsInterviewPath(t *testing.T) {
	store := &fakeFinalizeStore{}
	synth := &fakeSynthesisRunner{output: agent.SynthesizerOutput{Tree: testInterviewNodes()}}
	status, err := finalizeInterview(context.Background(), finalizerDeps{
		store:       store,
		analyst:     &fakeInterviewAnalyst{output: testAnalystOutput()},
		synthesizer: synth,
	}, "run-2", "Аналитик данных", testFinalizeSession())
	if err != nil || status != domain.RunDone {
		t.Fatalf("finalize = %v, %v; want done", status, err)
	}
	if len(synth.input.Sources) != 0 {
		t.Fatalf("sources = %+v, want none", synth.input.Sources)
	}
	if len(synth.input.Interview) != len(testInterviewNodes()) {
		t.Fatalf("interview nodes = %d", len(synth.input.Interview))
	}
	if store.reviewRunID != "run-2" {
		t.Fatalf("review was not persisted for interview-only run: %q", store.reviewRunID)
	}
	if len(store.reviewConflicts) != 0 || len(store.reviewMessages) != 0 {
		t.Fatalf("unexpected review data: %+v / %+v", store.reviewConflicts, store.reviewMessages)
	}
	if len(store.savedTree) != 2 {
		t.Fatalf("saved tree = %d nodes", len(store.savedTree))
	}
}

func TestFinalizeInterviewSourceLoadErrorIsNonFatal(t *testing.T) {
	store := &fakeFinalizeStore{sourcesErr: errors.New("db is down")}
	synth := &fakeSynthesisRunner{output: agent.SynthesizerOutput{Tree: testInterviewNodes()}}
	status, err := finalizeInterview(context.Background(), finalizerDeps{
		store:       store,
		analyst:     &fakeInterviewAnalyst{output: testAnalystOutput()},
		synthesizer: synth,
	}, "run-3", "Аналитик данных", testFinalizeSession())
	if err != nil || status != domain.RunDone {
		t.Fatalf("finalize = %v, %v; want done despite source loading error", status, err)
	}
	if len(synth.input.Sources) != 0 {
		t.Fatalf("sources = %+v, want none", synth.input.Sources)
	}
}

func TestFinalizeInterviewAnalystErrorFailsRun(t *testing.T) {
	store := &fakeFinalizeStore{}
	synth := &fakeSynthesisRunner{output: testSynthesisOutput()}
	status, err := finalizeInterview(context.Background(), finalizerDeps{
		store:       store,
		analyst:     &fakeInterviewAnalyst{err: errors.New("model unavailable")},
		synthesizer: synth,
	}, "run-4", "Аналитик данных", testFinalizeSession())
	if status != domain.RunFailed || err == nil {
		t.Fatalf("finalize = %v, %v; want failed", status, err)
	}
	if synth.called {
		t.Fatal("synthesizer must not run when analyst fails")
	}
	if store.reviewRunID != "" || len(store.savedTree) != 0 {
		t.Fatalf("nothing should be persisted after analyst failure: review=%q tree=%d", store.reviewRunID, len(store.savedTree))
	}
}

func TestFinalizeInterviewSynthesizerErrorFailsRun(t *testing.T) {
	store := &fakeFinalizeStore{}
	status, err := finalizeInterview(context.Background(), finalizerDeps{
		store:       store,
		analyst:     &fakeInterviewAnalyst{output: testAnalystOutput()},
		synthesizer: &fakeSynthesisRunner{err: errors.New("merge failed")},
	}, "run-5", "Аналитик данных", testFinalizeSession())
	if status != domain.RunFailed || err == nil {
		t.Fatalf("finalize = %v, %v; want failed", status, err)
	}
	if store.reviewRunID != "" || len(store.savedTree) != 0 {
		t.Fatalf("nothing should be persisted after synthesis failure: review=%q tree=%d", store.reviewRunID, len(store.savedTree))
	}
}

func TestFinalizeInterviewRequiresStore(t *testing.T) {
	status, err := finalizeInterview(context.Background(), finalizerDeps{
		analyst:     &fakeInterviewAnalyst{output: testAnalystOutput()},
		synthesizer: &fakeSynthesisRunner{output: testSynthesisOutput()},
	}, "run-6", "Аналитик данных", testFinalizeSession())
	if status != domain.RunFailed || err == nil {
		t.Fatalf("finalize = %v, %v; want failed", status, err)
	}
}

func TestFinalizeInterviewReviewPersistErrorDoesNotFailRun(t *testing.T) {
	store := &fakeFinalizeStore{reviewErr: errors.New("agent_messages unavailable")}
	status, err := finalizeInterview(context.Background(), finalizerDeps{
		store:       store,
		analyst:     &fakeInterviewAnalyst{output: testAnalystOutput()},
		synthesizer: &fakeSynthesisRunner{output: testSynthesisOutput()},
	}, "run-7", "Аналитик данных", testFinalizeSession())
	if err != nil || status != domain.RunDone {
		t.Fatalf("finalize = %v, %v; want done despite review persistence error", status, err)
	}
	if len(store.savedTree) != 2 {
		t.Fatalf("saved tree = %d nodes, want tree saved", len(store.savedTree))
	}
}

// TestNewTreeFinalizerWithVersionsWiresRealAgents проверяет production-обвязку:
// реальные analyst/synthesizer на фейковом LLM-клиенте, источники run доходят
// до детерминированного merge, дерево и review сохраняются.
func TestNewTreeFinalizerWithVersionsWiresRealAgents(t *testing.T) {
	analystAnswer := `{"entities":[
{"id":"role-analyst","type":"role","title":"Аналитик данных","evidence":[{"id":"ev-1","source_id":"src-interview","ref_id":"q1","quote":"Я аналитик данных."}]},
{"id":"fn-reports","type":"function","title":"Отчётность","parent_id":"role-analyst","evidence":[{"id":"ev-2","source_id":"src-interview","ref_id":"q2","quote":"Строю отчёты."}]}
]}`
	client := &fakeChatClient{responses: []string{analystAnswer, "{}"}}
	store := &fakeFinalizeStore{sources: []domain.Source{{ID: "src-vacancy-1", Type: domain.SourceVacancy, Title: "Вакансия"}}}
	finalizer := newTreeFinalizerWithVersions(store, client, "", "", nil)

	status, err := finalizer(context.Background(), "run-8", "Аналитик данных", testFinalizeSession())
	if err != nil || status != domain.RunDone {
		t.Fatalf("finalize = %v, %v; want done", status, err)
	}
	if len(store.savedTree) == 0 {
		t.Fatal("real synthesizer output was not saved")
	}
	if store.reviewRunID != "run-8" {
		t.Fatalf("review was not persisted: %q", store.reviewRunID)
	}
	if len(store.reviewConflicts) != 0 || len(store.reviewMessages) != 0 {
		t.Fatalf("only one source is present, conflicts must be empty: %+v", store.reviewConflicts)
	}
}
