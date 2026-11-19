package steps

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"activity-descriptor/internal/agent/synthesizer"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
)

const validAnalystJSON = `{"entities":[{"id":"role-1","type":"role","title":"Аналитик данных","evidence":[{"id":"e1","source_id":"src-interview","ref_id":"q-1","quote":"готовлю отчёты"}]}]}`

// fakeAnalystClient returns one valid analyst answer. calls counts model
// requests so retry behaviour can be asserted.
type fakeAnalystClient struct {
	mu    sync.Mutex
	calls int
	err   error
	block bool
}

func (c *fakeAnalystClient) Chat(ctx context.Context, _ []llm.Message) (string, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	if c.block {
		<-ctx.Done()
		return "", ctx.Err()
	}
	if c.err != nil {
		return "", c.err
	}
	return validAnalystJSON, nil
}

func (c *fakeAnalystClient) CallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

type fakeSource struct {
	runID      string
	transcript string
	sourceType domain.SourceType
}

// fakeAITreeStore implements steps.AITreeStore, steps.TranscriptStore and
// domain.Store (Save) so it can stand in for *api.PGStore in orchestrator tests.
type fakeAITreeStore struct {
	mu         sync.Mutex
	sources    map[string]fakeSource
	links      map[string][]string
	saved      map[string][]domain.Node
	lastRun    map[string]domain.Run
	createErr  error
	linkErr    error
	saveErr    error
	created    int
	treeExists bool
}

func newFakeAITreeStore() *fakeAITreeStore {
	return &fakeAITreeStore{
		sources: map[string]fakeSource{},
		links:   map[string][]string{},
		saved:   map[string][]domain.Node{},
		lastRun: map[string]domain.Run{},
	}
}

func (s *fakeAITreeStore) Save(run *domain.Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := *run
	s.lastRun[run.ID] = copied
	return nil
}

func (s *fakeAITreeStore) LastRun(runID string) (domain.Run, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.lastRun[runID]
	return run, ok
}

func (s *fakeAITreeStore) TreeExists(context.Context, string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.treeExists, nil
}

func (s *fakeAITreeStore) SaveTree(_ context.Context, runID string, nodes []domain.Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return s.saveErr
	}
	copied := make([]domain.Node, len(nodes))
	copy(copied, nodes)
	s.saved[runID] = copied
	return nil
}

func (s *fakeAITreeStore) CreateInterviewSource(_ context.Context, runID, sourceID, transcript string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.createErr != nil {
		return s.createErr
	}
	s.created++
	s.sources[sourceID] = fakeSource{runID: runID, transcript: transcript, sourceType: domain.SourceInterview}
	return nil
}

func (s *fakeAITreeStore) LinkRunSources(_ context.Context, runID string, sourceIDs []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.linkErr != nil {
		return s.linkErr
	}
	s.links[runID] = append(s.links[runID], sourceIDs...)
	return nil
}

func (s *fakeAITreeStore) ListRunSourceContents(_ context.Context, runID string) ([]domain.Source, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.Source, 0, len(s.links[runID]))
	for _, sourceID := range s.links[runID] {
		source, ok := s.sources[sourceID]
		if !ok {
			continue
		}
		out = append(out, domain.Source{ID: sourceID, Type: source.sourceType, Content: source.transcript})
	}
	return out, nil
}

func (s *fakeAITreeStore) Saved(runID string) []domain.Node {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saved[runID]
}

// fakeVacancyReader provides deterministic vacancy data for the fallback chain.
type fakeVacancyReader struct{ empty bool }

func (r fakeVacancyReader) ByProfession(_ context.Context, _ string, _ int) ([]domain.VacancyRecord, error) {
	if r.empty {
		return nil, nil
	}
	return []domain.VacancyRecord{{
		ID:          "v1",
		SourceID:    "11111111-1111-4111-8111-111111111111",
		Title:       "Go Developer",
		Description: "go docker kubernetes python sql",
	}}, nil
}

func newRealSynthesizer(t *testing.T) Synthesizer {
	t.Helper()
	agentInstance, err := synthesizer.New()
	if err != nil {
		t.Fatalf("synthesizer.New: %v", err)
	}
	return agentInstance
}

type staticSourceLoader struct {
	sources []domain.Source
}

func (l staticSourceLoader) ListRunSourceContents(context.Context, string) ([]domain.Source, error) {
	return l.sources, nil
}

func (l staticSourceLoader) ListGlobalSources(int) ([]domain.Source, error) {
	return l.sources, nil
}

func TestAnalysisSourcesMergesMaterialsRunSourcesAndVacancies(t *testing.T) {
	step := &AgentAnalysisStep{
		GlobalSources: staticSourceLoader{sources: []domain.Source{
			{ID: "doc-1", Type: domain.SourceDocument, Title: "Материал", Content: "Практика"},
			{ID: "interview-1", Type: domain.SourceInterview, Content: "дубль транскрипта"},
		}},
		RunSources: staticSourceLoader{sources: []domain.Source{
			{ID: "doc-1", Type: domain.SourceDocument, Title: "Материал", Content: "Практика"},
			{ID: "std-1", Type: domain.SourceStandard, Title: "Стандарт", Content: "Требования"},
		}},
	}
	sources := step.analysisSources(context.Background(), "run-1", []domain.VacancyRecord{{
		ID: "v1", SourceID: "vac-1", Title: "Backend", Description: "Go и SQL",
	}})
	if len(sources) != 3 {
		t.Fatalf("sources = %#v, want 3", sources)
	}
	ids := map[string]bool{}
	for _, source := range sources {
		ids[source.ID] = true
	}
	if !ids["doc-1"] || !ids["std-1"] || !ids["vac-1"] {
		t.Fatalf("missing source ids: %#v", ids)
	}
}

func TestAgentAnalysisAndSynthesisSteps(t *testing.T) {
	run := &domain.Run{ID: "run-1", Profession: "Аналитик данных"}
	SetTranscript(run, "Я аналитик данных и готовлю отчёты.", "")

	analysis := &AgentAnalysisStep{Client: &fakeAnalystClient{}}
	if err := analysis.ExecuteWithData(context.Background(), run, nil); err != nil {
		t.Fatalf("analysis: %v", err)
	}
	nodes, ok := run.Data[DataKeyInterviewNodes].([]domain.Node)
	if !ok || len(nodes) == 0 {
		t.Fatalf("interview_nodes = %#v", run.Data[DataKeyInterviewNodes])
	}

	synthesis := &AgentSynthesisStep{Synthesizer: newRealSynthesizer(t)}
	if err := synthesis.Execute(context.Background(), run); err != nil {
		t.Fatalf("synthesis: %v", err)
	}
	tree, ok := run.Data[DataKeyTree].([]domain.Node)
	if !ok || len(tree) == 0 {
		t.Fatalf("tree = %#v", run.Data[DataKeyTree])
	}
	if _, ok := run.Data[DataKeyConflicts].([]domain.Conflict); !ok {
		t.Fatalf("conflicts = %#v", run.Data[DataKeyConflicts])
	}
}

func TestAgentAnalysisStepRequiresTranscript(t *testing.T) {
	run := &domain.Run{ID: "run-2", Profession: "Аналитик"}
	step := &AgentAnalysisStep{Client: &fakeAnalystClient{}}
	if err := step.ExecuteWithData(context.Background(), run, nil); err == nil {
		t.Fatal("expected error for empty transcript")
	}
	if err := step.ExecuteWithData(context.Background(), nil, nil); err == nil {
		t.Fatal("expected error for nil run")
	}
}

func TestAgentAnalysisStepModelError(t *testing.T) {
	run := &domain.Run{ID: "run-3", Profession: "Аналитик"}
	SetTranscript(run, "Я аналитик данных и готовлю отчёты.", "")
	step := &AgentAnalysisStep{Client: &fakeAnalystClient{err: errors.New("provider down")}}
	if err := step.ExecuteWithData(context.Background(), run, nil); err == nil {
		t.Fatal("expected model error")
	}
}

func TestAgentSynthesisStepRequiresInterviewNodes(t *testing.T) {
	step := &AgentSynthesisStep{}
	if err := step.Execute(context.Background(), &domain.Run{ID: "run-4"}); err == nil {
		t.Fatal("expected error for missing interview_nodes")
	}
	if err := step.Execute(context.Background(), nil); err == nil {
		t.Fatal("expected error for nil run")
	}
}

func TestSaveAITreeStepCreatesSourceAndUUIDs(t *testing.T) {
	run := &domain.Run{ID: "run-5", Data: map[string]any{
		DataKeyTranscript: "Я аналитик данных и готовлю отчёты.",
		DataKeyTree: []domain.Node{{
			ID:    "model-node-1",
			Type:  domain.NodeRole,
			Title: "Аналитик данных",
			Evidence: []domain.Evidence{{
				ID: "model-evidence-1", SourceID: "interview-transcript", RefID: "q-1", Quote: "готовлю отчёты",
			}},
		}},
	}}
	store := newFakeAITreeStore()
	step := &SaveAITreeStep{Store: store, NewID: sequenceUUID()}
	if err := step.Execute(context.Background(), run); err != nil {
		t.Fatalf("save: %v", err)
	}

	if store.created != 1 {
		t.Fatalf("created sources = %d, want 1", store.created)
	}
	sourceID, _ := run.Data[DataKeyInterviewSourceID].(string)
	if sourceID == "" {
		t.Fatal("interview source id was not stored in run.Data")
	}
	saved := store.Saved(run.ID)
	if len(saved) != 1 {
		t.Fatalf("saved nodes = %d, want 1", len(saved))
	}
	if err := domain.ValidateTree(saved); err != nil {
		t.Fatalf("saved tree invalid: %v", err)
	}
	rootCount := 0
	for _, node := range saved {
		if node.ParentID == nil {
			rootCount++
		}
	}
	if rootCount != 1 {
		t.Fatalf("roots = %d, want 1", rootCount)
	}
	node := saved[0]
	if node.ID == "model-node-1" {
		t.Fatal("node id was not replaced with a UUID")
	}
	if len(node.Evidence) != 1 {
		t.Fatalf("evidence = %+v", node.Evidence)
	}
	if node.Evidence[0].SourceID != sourceID {
		t.Fatalf("evidence source = %q, want %q", node.Evidence[0].SourceID, sourceID)
	}
	if node.Evidence[0].ID == "model-evidence-1" {
		t.Fatal("evidence id was not replaced with a UUID")
	}
	if node.Evidence[0].Quote != "готовлю отчёты" || node.Evidence[0].RefID != "q-1" {
		t.Fatalf("evidence content changed: %+v", node.Evidence[0])
	}
}

func TestSaveAITreeStepReusesPersistedSource(t *testing.T) {
	persistedSourceID := "33333333-3333-4333-8333-333333333333"
	run := &domain.Run{ID: "run-6", Data: map[string]any{
		DataKeyTranscript:        "готовлю отчёты",
		DataKeyInterviewSourceID: persistedSourceID,
		DataKeyTree: []domain.Node{{
			ID: "n1", Type: domain.NodeRole, Title: "Роль",
			Evidence: []domain.Evidence{{ID: "e1", SourceID: "interview-transcript", Quote: "готовлю отчёты"}},
		}},
	}}
	store := newFakeAITreeStore()
	if err := (&SaveAITreeStep{Store: store, NewID: sequenceUUID()}).Execute(context.Background(), run); err != nil {
		t.Fatalf("save: %v", err)
	}
	if store.created != 0 {
		t.Fatalf("created sources = %d, want 0", store.created)
	}
	saved := store.Saved(run.ID)
	if len(saved) != 1 || saved[0].Evidence[0].SourceID != persistedSourceID {
		t.Fatalf("saved = %+v", saved)
	}
}

func TestSaveAITreeStepRepairsHierarchy(t *testing.T) {
	// Two model roots and a shallow child: RepairTree keeps one role root and
	// gives every node a type matching its depth.
	run := &domain.Run{ID: "run-7", Data: map[string]any{
		DataKeyTranscript: "готовлю отчёты",
		DataKeyTree: []domain.Node{
			{ID: "root", Type: domain.NodeRole, Title: "Аналитик", Evidence: []domain.Evidence{{ID: "e1", SourceID: "x", Quote: "q"}}},
			{ID: "orphan", Type: domain.NodeAction, Title: "Отчёты", Evidence: []domain.Evidence{{ID: "e2", SourceID: "x", Quote: "q"}}},
			{ID: "child", Type: domain.NodeRole, Title: "Функция", ParentID: stringPtr("root"), Evidence: []domain.Evidence{{ID: "e3", SourceID: "x", Quote: "q"}}},
		},
	}}
	store := newFakeAITreeStore()
	if err := (&SaveAITreeStep{Store: store, NewID: sequenceUUID()}).Execute(context.Background(), run); err != nil {
		t.Fatalf("save: %v", err)
	}
	saved := store.Saved(run.ID)
	if err := domain.ValidateTree(saved); err != nil {
		t.Fatalf("repaired tree invalid: %v", err)
	}
	roots := 0
	for _, node := range saved {
		if node.ParentID == nil {
			roots++
		}
	}
	if roots != 1 {
		t.Fatalf("roots = %d, want 1", roots)
	}
}

func TestSaveAITreeStepErrors(t *testing.T) {
	baseData := func() map[string]any {
		return map[string]any{
			DataKeyTranscript: "готовлю отчёты",
			DataKeyTree: []domain.Node{{
				ID: "n1", Type: domain.NodeRole, Title: "Роль",
				Evidence: []domain.Evidence{{ID: "e1", SourceID: "x", Quote: "q"}},
			}},
		}
	}
	cases := []struct {
		name string
		step *SaveAITreeStep
		run  *domain.Run
	}{
		{name: "nil run", step: &SaveAITreeStep{Store: newFakeAITreeStore()}, run: nil},
		{name: "nil store", step: &SaveAITreeStep{}, run: &domain.Run{ID: "r", Data: baseData()}},
		{name: "empty tree", step: &SaveAITreeStep{Store: newFakeAITreeStore()}, run: &domain.Run{ID: "r", Data: map[string]any{DataKeyTranscript: "q"}}},
		{name: "missing transcript", step: &SaveAITreeStep{Store: newFakeAITreeStore()}, run: &domain.Run{ID: "r", Data: map[string]any{DataKeyTree: baseData()[DataKeyTree]}}},
		{name: "node without id", step: &SaveAITreeStep{Store: newFakeAITreeStore()}, run: &domain.Run{ID: "r", Data: map[string]any{
			DataKeyTranscript: "q",
			DataKeyTree:       []domain.Node{{Type: domain.NodeRole, Title: "Роль", Evidence: []domain.Evidence{{ID: "e", SourceID: "x", Quote: "q"}}}},
		}}},
		{name: "create error", step: &SaveAITreeStep{Store: func() *fakeAITreeStore {
			store := newFakeAITreeStore()
			store.createErr = errors.New("create failed")
			return store
		}()}, run: &domain.Run{ID: "r", Data: baseData()}},
		{name: "save error", step: &SaveAITreeStep{Store: func() *fakeAITreeStore {
			store := newFakeAITreeStore()
			store.saveErr = errors.New("save failed")
			return store
		}(), NewID: sequenceUUID()}, run: &domain.Run{ID: "r", Data: baseData()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.step.Execute(context.Background(), tc.run); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func stringPtr(value string) *string { return &value }

// sequenceUUID returns a deterministic UUID generator for tests. The generated
// values are canonical v4 UUIDs so database-style validation passes.
func sequenceUUID() func() string {
	var counter int
	return func() string {
		counter++
		return fmt.Sprintf("00000000-0000-4000-8000-%012d", counter)
	}
}
