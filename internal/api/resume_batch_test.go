package api

import (
	"context"
	"sync"
	"testing"

	"activity-descriptor/internal/agent/synthesizer"
	"activity-descriptor/internal/domain"
	"activity-descriptor/internal/llm"
	"activity-descriptor/internal/steps"
)

const resumeAnalystJSON = `{"entities":[{"id":"role-1","type":"role","title":"Аналитик данных","evidence":[{"id":"e1","source_id":"interview-transcript","ref_id":"q-1","quote":"готовлю отчёты"}]}]}`

type resumeAnalystClient struct {
	mu    sync.Mutex
	calls int
}

func (c *resumeAnalystClient) Chat(context.Context, []llm.Message) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return resumeAnalystJSON, nil
}

func (c *resumeAnalystClient) CallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

type resumeSource struct {
	content    string
	sourceType domain.SourceType
}

// batchResumeStore is an in-memory IncompleteLister + domain.Store + AITreeStore
// + TranscriptStore used to verify that the resume processor runs the same batch
// plan as the create-run handler.
type batchResumeStore struct {
	mu      sync.Mutex
	runs    map[string]*domain.Run
	sources map[string]resumeSource
	links   map[string][]string
	saved   map[string][]domain.Node
}

func newBatchResumeStore() *batchResumeStore {
	return &batchResumeStore{
		runs:    map[string]*domain.Run{},
		sources: map[string]resumeSource{},
		links:   map[string][]string{},
		saved:   map[string][]domain.Node{},
	}
}

func (s *batchResumeStore) seedRun(run domain.Run) {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := run
	s.runs[run.ID] = &copied
}

func (s *batchResumeStore) ListIncompleteRuns() ([]*domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []*domain.Run{}
	for _, run := range s.runs {
		if run.Status != domain.RunRunning || run.Stage == domain.StageDone {
			continue
		}
		copied := *run
		out = append(out, &copied)
	}
	return out, nil
}

func (s *batchResumeStore) Save(run *domain.Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := *run
	s.runs[run.ID] = &copied
	return nil
}

func (s *batchResumeStore) SaveTree(_ context.Context, runID string, nodes []domain.Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := make([]domain.Node, len(nodes))
	copy(copied, nodes)
	s.saved[runID] = copied
	return nil
}

func (s *batchResumeStore) CreateInterviewSource(_ context.Context, runID, sourceID, transcript string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Mirrors PGStore.CreateInterviewSource: the source row is created with
	// run_id, but run_sources is linked by LinkRunSources only.
	_ = runID
	s.sources[sourceID] = resumeSource{content: transcript, sourceType: domain.SourceInterview}
	return nil
}

func (s *batchResumeStore) LinkRunSources(_ context.Context, runID string, sourceIDs []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.links[runID] = append(s.links[runID], sourceIDs...)
	return nil
}

func (s *batchResumeStore) ListRunSourceContents(_ context.Context, runID string) ([]domain.Source, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []domain.Source{}
	for _, sourceID := range s.links[runID] {
		source, ok := s.sources[sourceID]
		if !ok {
			continue
		}
		out = append(out, domain.Source{ID: sourceID, Type: source.sourceType, Content: source.content})
	}
	return out, nil
}

func (s *batchResumeStore) Saved(runID string) []domain.Node {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saved[runID]
}

func (s *batchResumeStore) Run(runID string) (*domain.Run, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[runID]
	if !ok {
		return nil, false
	}
	copied := *run
	return &copied, true
}

func newResumeBatchPlan(t *testing.T, store *batchResumeStore, client llm.ChatClient) steps.BatchPlan {
	t.Helper()
	synth, err := synthesizer.New()
	if err != nil {
		t.Fatalf("synthesizer.New: %v", err)
	}
	return steps.NewBatchPlan(steps.BatchOptions{
		Analyst:     client,
		Synthesizer: synth,
		Store:       store,
		VacancyTopK: 10,
	})
}

func TestResumeUsesAIBatchPlanAndPersistedTranscript(t *testing.T) {
	store := newBatchResumeStore()
	run := domain.Run{ID: "run-resume-ai", Status: domain.RunRunning, Stage: domain.StageAnalysis, Profession: "Аналитик данных"}
	store.seedRun(run)
	transcript := "Я аналитик данных и готовлю отчёты."
	if _, err := steps.PersistInterviewTranscript(context.Background(), store, run.ID, transcript); err != nil {
		t.Fatalf("persist transcript: %v", err)
	}

	client := &resumeAnalystClient{}
	plan := newResumeBatchPlan(t, store, client)
	if !plan.HasAI() {
		t.Fatal("expected AI plan")
	}
	rp := NewResumeProcessor(store, store, mockVacancyReader{})
	rp.SetBatchPlan(plan)

	if err := rp.ProcessIncompleteRuns(context.Background()); err != nil {
		t.Fatalf("resume: %v", err)
	}

	if client.CallCount() == 0 {
		t.Fatal("resume did not run the AI analyst")
	}
	saved := store.Saved(run.ID)
	if len(saved) == 0 {
		t.Fatal("resume did not persist the AI tree")
	}
	if err := domain.ValidateTree(saved); err != nil {
		t.Fatalf("saved tree invalid: %v", err)
	}
	for _, node := range saved {
		for _, evidence := range node.Evidence {
			if evidence.SourceID == "" {
				t.Fatalf("evidence without source: %+v", evidence)
			}
		}
	}
	loaded, ok := store.Run(run.ID)
	if !ok || loaded.Status != domain.RunDone || loaded.Stage != domain.StageDone {
		t.Fatalf("run after resume = %+v", loaded)
	}
	// The persisted interview source is reused, not duplicated by the review step.
	if got := len(store.links[run.ID]); got != 1 {
		t.Fatalf("interview sources = %d, want 1", got)
	}
}

func TestResumeFallsBackToVacancyWithoutTranscript(t *testing.T) {
	store := newBatchResumeStore()
	run := domain.Run{ID: "run-resume-vacancy", Status: domain.RunRunning, Stage: domain.StageAnalysis, Profession: "Go Developer"}
	store.seedRun(run)

	client := &resumeAnalystClient{}
	plan := newResumeBatchPlan(t, store, client)
	rp := NewResumeProcessor(store, store, mockVacancyReader{})
	rp.SetBatchPlan(plan)

	if err := rp.ProcessIncompleteRuns(context.Background()); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if client.CallCount() != 0 {
		t.Fatalf("vacancy fallback must not call the analyst, calls = %d", client.CallCount())
	}
	if saved := store.Saved(run.ID); len(saved) == 0 {
		t.Fatal("vacancy fallback did not persist a tree")
	}
	loaded, ok := store.Run(run.ID)
	if !ok || loaded.Status != domain.RunDone || loaded.Stage != domain.StageDone {
		t.Fatalf("run after resume = %+v", loaded)
	}
}

func TestServerBatchPipelinePrefersConfiguredPlan(t *testing.T) {
	server := NewServer(newMemStore())
	store := newBatchResumeStore()
	if plan := server.batchPipeline(store); plan.HasAI() || len(plan.Fallback) == 0 {
		t.Fatalf("default plan = %+v, want vacancy fallback", plan)
	}
	plan := newResumeBatchPlan(t, store, &resumeAnalystClient{})
	server.SetBatchPlan(plan)
	if got := server.batchPipeline(store); !got.HasAI() {
		t.Fatal("configured AI plan was not returned")
	}
}
