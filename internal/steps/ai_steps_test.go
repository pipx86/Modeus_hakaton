package steps

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"activity-descriptor/internal/agent"
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
	mu              sync.Mutex
	sources         map[string]fakeSource
	links           map[string][]string
	saved           map[string][]domain.Node
	lastRun         map[string]domain.Run
	createErr       error
	linkErr         error
	saveErr         error
	reviewErr       error
	created         int
	treeExists      bool
	reviewRunID     string
	reviewConflicts []domain.Conflict
	reviewMessages  []agent.AgentMessage
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

// SaveSynthesisReview implements the optional review-persistence capability, so
// the batch review step exercises the same conflict storage as *api.PGStore.
func (s *fakeAITreeStore) SaveSynthesisReview(_ context.Context, runID string, conflicts []domain.Conflict, messages []agent.AgentMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reviewErr != nil {
		return s.reviewErr
	}
	s.reviewRunID = runID
	s.reviewConflicts = append([]domain.Conflict(nil), conflicts...)
	s.reviewMessages = append([]agent.AgentMessage(nil), messages...)
	return nil
}

// Review returns the last persisted synthesis review of a run.
func (s *fakeAITreeStore) Review(runID string) (string, []domain.Conflict, []agent.AgentMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reviewRunID != runID {
		return "", nil, nil
	}
	return s.reviewRunID, s.reviewConflicts, s.reviewMessages
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
	sources := step.analysisSources(context.Background(), "run-1", "Backend-разработчик", []domain.VacancyRecord{{
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

func TestAnalysisSourcesIncludesAllProfessions(t *testing.T) {
	step := &AgentAnalysisStep{
		GlobalSources: staticSourceLoader{sources: []domain.Source{
			{ID: "generic", Type: domain.SourceDocument, Title: "Общая методика", Content: "Как проводить встречи"},
			{ID: "data", Type: domain.SourceStandard, Title: "Аналитик данных", Content: "SQL и дашборды"},
		}},
	}
	sources := step.analysisSources(context.Background(), "run-2", "Backend-разработчик", []domain.VacancyRecord{{
		ID: "v2", SourceID: "vac-2", Title: "Backend Engineer", Description: "Go и PostgreSQL",
	}})
	if len(sources) != 3 {
		t.Fatalf("sources = %#v, want all three sources", sources)
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

	synthesis := &AgentSynthesisStep{
		Synthesizer: newRealSynthesizer(t),
		ZunEnricher: func(_ context.Context, _ string, nodes []domain.Node) []domain.Node {
			for index := range nodes {
				nodes[index].Zun = &domain.Zun{Knowledge: []string{"Тестовое знание"}}
			}
			return nodes
		},
	}
	if err := synthesis.Execute(context.Background(), run); err != nil {
		t.Fatalf("synthesis: %v", err)
	}
	tree, ok := run.Data[DataKeyTree].([]domain.Node)
	if !ok || len(tree) == 0 {
		t.Fatalf("tree = %#v", run.Data[DataKeyTree])
	}
	if tree[0].Zun == nil || len(tree[0].Zun.Knowledge) == 0 {
		t.Fatalf("ZUN enricher was not applied: %#v", tree[0])
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

func TestSaveAITreeStepPreservesVacancyEvidenceSource(t *testing.T) {
	run := &domain.Run{ID: "run-vacancy-source", Data: map[string]any{
		DataKeyTranscript: "Я аналитик",
		DataKeySourceIDs:  map[string]string{"vac-1": "vac-1"},
		DataKeyTree: []domain.Node{{
			ID: "n1", Type: domain.NodeRole, Title: "Роль",
			Evidence: []domain.Evidence{{ID: "e1", SourceID: "vac-1", Quote: "Go"}},
		}},
	}}
	store := newFakeAITreeStore()
	if err := (&SaveAITreeStep{Store: store, NewID: sequenceUUID()}).Execute(context.Background(), run); err != nil {
		t.Fatalf("save: %v", err)
	}
	saved := store.Saved(run.ID)
	if len(saved) != 1 || len(saved[0].Evidence) != 1 {
		t.Fatalf("saved = %+v", saved)
	}
	if saved[0].Evidence[0].SourceID != "vac-1" {
		t.Fatalf("evidence source = %q, want vac-1", saved[0].Evidence[0].SourceID)
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

// TestAgentAnalysisStepStoresSourceMetadata verifies that the batch analyst step
// keeps the declared provenance of the materials it was given, so the synthesis
// step can route the resulting nodes by evidence instead of collapsing every
// source into the Interview slot.
func TestAgentAnalysisStepStoresSourceMetadata(t *testing.T) {
	run := &domain.Run{ID: "run-source-meta", Profession: "Аналитик данных"}
	SetTranscript(run, "Я аналитик данных и готовлю отчёты.", "")
	step := &AgentAnalysisStep{
		Client: &fakeAnalystClient{},
		RunSources: staticSourceLoader{sources: []domain.Source{
			{ID: "src-standard", Type: domain.SourceStandard, Title: "Профстандарт аналитика данных", Content: "Аналитик данных готовит отчёты"},
			{ID: "src-vacancy", Type: domain.SourceVacancy, Title: "Вакансия аналитика данных", Content: "Вакансия: аналитик данных готовит отчёты"},
		}},
	}
	if err := step.ExecuteWithData(context.Background(), run, nil); err != nil {
		t.Fatalf("analysis: %v", err)
	}

	metadata, ok := run.Data[DataKeySourceMetadata].([]domain.Source)
	if !ok || len(metadata) != 2 {
		t.Fatalf("source metadata = %#v", run.Data[DataKeySourceMetadata])
	}
	types := map[string]domain.SourceType{}
	for _, source := range metadata {
		types[source.ID] = source.Type
		if source.Content != "" {
			t.Fatalf("source metadata must not keep content: %+v", source)
		}
	}
	if types["src-standard"] != domain.SourceStandard || types["src-vacancy"] != domain.SourceVacancy {
		t.Fatalf("source types = %#v, want standard and vacancy", types)
	}
}

// TestAgentSynthesisStepDetectsCrossSourceConflict is the batch counterpart of
// the chat finalizer conflict test: nodes extracted from a standard and a vacancy
// share one id, so routing them into their own slots must produce a resolved
// conflict instead of silently keeping whichever value came first.
func TestAgentSynthesisStepDetectsCrossSourceConflict(t *testing.T) {
	const (
		standardSource = "std-source"
		vacancySource  = "vac-source"
		nodeID         = "role-1"
	)
	run := &domain.Run{ID: "run-batch-conflict", Profession: "Аналитик данных", Data: map[string]any{
		DataKeySourceMetadata: []domain.Source{
			{ID: standardSource, Type: domain.SourceStandard, Title: "Профстандарт", Priority: domain.PriorityStandard},
			{ID: vacancySource, Type: domain.SourceVacancy, Title: "Вакансия", Priority: domain.PriorityVacancy},
		},
		DataKeyInterviewNodes: []domain.Node{
			{ID: nodeID, Type: domain.NodeRole, Title: "Аналитик данных",
				Evidence: []domain.Evidence{{ID: "ev-std", SourceID: standardSource, RefID: "req-1", Quote: "роль Аналитик данных"}}},
			{ID: nodeID, Type: domain.NodeRole, Title: "Data Analyst",
				Evidence: []domain.Evidence{{ID: "ev-vac", SourceID: vacancySource, RefID: "line-1", Quote: "Вакансия Data Analyst"}}},
		},
	}}

	step := &AgentSynthesisStep{Synthesizer: newRealSynthesizer(t)}
	if err := step.Execute(context.Background(), run); err != nil {
		t.Fatalf("synthesis: %v", err)
	}

	conflicts, ok := run.Data[DataKeyConflicts].([]domain.Conflict)
	if !ok || len(conflicts) != 1 {
		t.Fatalf("conflicts = %#v, want one resolved title conflict", run.Data[DataKeyConflicts])
	}
	conflict := conflicts[0]
	if conflict.NodeID != nodeID || conflict.Field != "title" {
		t.Fatalf("conflict = %+v, want the shared role title", conflict)
	}
	if len(conflict.Values) != 2 {
		t.Fatalf("conflict values = %+v, want both source positions", conflict.Values)
	}
	values := map[string]domain.ConflictValue{}
	for _, value := range conflict.Values {
		values[value.SourceType.String()+"/"+value.Value] = value
	}
	if _, ok := values["standard/Аналитик данных"]; !ok {
		t.Fatalf("standard position missing from %+v", conflict.Values)
	}
	if _, ok := values["vacancy/Data Analyst"]; !ok {
		t.Fatalf("losing vacancy position missing from %+v", conflict.Values)
	}
	if conflict.Winner.SourceType != domain.SourceStandard || conflict.Winner.SourceID != standardSource {
		t.Fatalf("winner = %+v, want the standard position", conflict.Winner)
	}

	// The merged tree keeps the winning wording and the evidence of both sources.
	tree, ok := run.Data[DataKeyTree].([]domain.Node)
	if !ok {
		t.Fatalf("tree = %#v", run.Data[DataKeyTree])
	}
	merged := false
	for _, node := range tree {
		if node.ID != nodeID {
			continue
		}
		merged = true
		if node.Title != "Аналитик данных" {
			t.Fatalf("merged title = %q, want the standard value", node.Title)
		}
		if len(node.Evidence) != 2 {
			t.Fatalf("merged evidence = %+v, want both sources", node.Evidence)
		}
	}
	if !merged {
		t.Fatalf("merged node %q missing from the tree", nodeID)
	}

	messages, ok := run.Data[DataKeyMessages].([]agent.AgentMessage)
	if !ok || len(messages) != 1 || messages[0].Kind != "conflict" {
		t.Fatalf("messages = %#v, want one conflict message", run.Data[DataKeyMessages])
	}
}

func batchConflict() domain.Conflict {
	return domain.Conflict{
		ID:     "c-1",
		NodeID: "role-1",
		Field:  "title",
		Values: []domain.ConflictValue{
			{SourceID: "src-standard", SourceType: domain.SourceStandard, Value: "Аналитик данных", Priority: domain.PriorityStandard},
			{SourceID: "src-vacancy", SourceType: domain.SourceVacancy, Value: "Data Analyst", Priority: domain.PriorityVacancy},
		},
		Winner: domain.ConflictValue{SourceID: "src-standard", SourceType: domain.SourceStandard, Value: "Аналитик данных", Priority: domain.PriorityStandard},
		Reason: "higher source priority wins: standard (200)",
	}
}

func batchMessages(conflict domain.Conflict) []agent.AgentMessage {
	return []agent.AgentMessage{{
		ID:   "msg-conflict-1",
		Kind: "conflict",
		Text: fmt.Sprintf("Конфликт %s узла %s", conflict.Field, conflict.NodeID),
	}}
}

func batchReviewRun() *domain.Run {
	conflict := batchConflict()
	return &domain.Run{ID: "run-review", Data: map[string]any{
		DataKeyTranscript: "готовлю отчёты",
		DataKeyTree: []domain.Node{{
			ID: "role-1", Type: domain.NodeRole, Title: "Аналитик данных",
			Evidence: []domain.Evidence{{ID: "e1", SourceID: "src-standard", RefID: "req-1", Quote: "роль Аналитик данных"}},
		}},
		DataKeyConflicts: []domain.Conflict{conflict},
		DataKeyMessages:  batchMessages(conflict),
	}}
}

// TestSaveAITreeStepPersistsSynthesisReview closes the batch half of карточка 9:
// the resolved conflict must reach the same storage the chat finalizer writes, so
// GET /runs/{id}/conflicts returns it for a batch run too.
func TestSaveAITreeStepPersistsSynthesisReview(t *testing.T) {
	run := batchReviewRun()
	store := newFakeAITreeStore()
	if err := (&SaveAITreeStep{Store: store, NewID: sequenceUUID()}).Execute(context.Background(), run); err != nil {
		t.Fatalf("save: %v", err)
	}

	runID, conflicts, messages := store.Review(run.ID)
	if runID != run.ID {
		t.Fatalf("review persisted for %q, want %q", runID, run.ID)
	}
	if len(conflicts) != 1 || conflicts[0].ID != "c-1" || len(conflicts[0].Values) != 2 {
		t.Fatalf("persisted conflicts = %+v, want the resolved conflict with both values", conflicts)
	}
	if len(messages) != 1 || messages[0].Kind != "conflict" {
		t.Fatalf("persisted messages = %+v, want the conflict message", messages)
	}
	if len(store.Saved(run.ID)) == 0 {
		t.Fatal("tree was not persisted")
	}
}

// TestSaveAITreeStepPersistsReviewBeforeTreeFailure pins the ordering used by the
// chat finalizer: the expert review survives a tree write failure.
func TestSaveAITreeStepPersistsReviewBeforeTreeFailure(t *testing.T) {
	run := batchReviewRun()
	store := newFakeAITreeStore()
	store.saveErr = errors.New("tree write failed")
	err := (&SaveAITreeStep{Store: store, NewID: sequenceUUID()}).Execute(context.Background(), run)
	if err == nil {
		t.Fatal("expected the tree write error")
	}
	if runID, conflicts, _ := store.Review(run.ID); runID != run.ID || len(conflicts) != 1 {
		t.Fatalf("review = %q / %+v, want the conflict persisted before the failed tree write", runID, conflicts)
	}
}

// TestSaveAITreeStepSkipsReviewWithoutSynthesis protects stored review rows: a
// step that never ran the synthesizer must not replace them with an empty list.
func TestSaveAITreeStepSkipsReviewWithoutSynthesis(t *testing.T) {
	run := &domain.Run{ID: "run-no-synthesis", Data: map[string]any{
		DataKeyTranscript: "готовлю отчёты",
		DataKeyTree: []domain.Node{{
			ID: "n1", Type: domain.NodeRole, Title: "Роль",
			Evidence: []domain.Evidence{{ID: "e1", SourceID: "x", Quote: "q"}},
		}},
	}}
	store := newFakeAITreeStore()
	if err := (&SaveAITreeStep{Store: store, NewID: sequenceUUID()}).Execute(context.Background(), run); err != nil {
		t.Fatalf("save: %v", err)
	}
	if runID, _, _ := store.Review(run.ID); runID != "" {
		t.Fatalf("review was rewritten without a synthesis step: %q", runID)
	}
}

// TestSaveAITreeStepToleratesReviewStorageFailure mirrors the chat finalizer: a
// review storage failure is logged and does not fail an otherwise valid run.
func TestSaveAITreeStepToleratesReviewStorageFailure(t *testing.T) {
	run := batchReviewRun()
	store := newFakeAITreeStore()
	store.reviewErr = errors.New("review storage down")
	if err := (&SaveAITreeStep{Store: store, NewID: sequenceUUID()}).Execute(context.Background(), run); err != nil {
		t.Fatalf("review storage failure must not fail the run: %v", err)
	}
	if len(store.Saved(run.ID)) == 0 {
		t.Fatal("tree was not persisted after a review storage failure")
	}
}

// sequenceUUID returns a deterministic UUID generator for tests. The generated
// values are canonical v4 UUIDs so database-style validation passes.
func sequenceUUID() func() string {
	var counter int
	return func() string {
		counter++
		return fmt.Sprintf("00000000-0000-4000-8000-%012d", counter)
	}
}
