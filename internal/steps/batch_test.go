package steps

import (
	"context"
	"errors"
	"testing"
	"time"

	"activity-descriptor/internal/domain"
)

var _ domain.Store = (*fakeAITreeStore)(nil)
var _ AITreeStore = (*fakeAITreeStore)(nil)
var _ TranscriptStore = (*fakeAITreeStore)(nil)

func stepNames(steps []domain.Step) []domain.Stage {
	out := make([]domain.Stage, 0, len(steps))
	for _, step := range steps {
		out = append(out, step.Name())
	}
	return out
}

func equalStages(got, want []domain.Stage) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestAIStepsFactory(t *testing.T) {
	store := newFakeAITreeStore()
	client := &fakeAnalystClient{}
	synth := newRealSynthesizer(t)

	if steps := AISteps(nil, synth, store, ""); steps != nil {
		t.Fatalf("nil analyst client must disable the AI chain: %v", stepNames(steps))
	}
	if steps := AISteps(client, nil, store, ""); steps != nil {
		t.Fatalf("nil synthesizer must disable the AI chain: %v", stepNames(steps))
	}
	if steps := AISteps(client, synth, nil, ""); steps != nil {
		t.Fatalf("nil store must disable the AI chain: %v", stepNames(steps))
	}

	steps := AISteps(client, synth, store, "v2")
	want := []domain.Stage{domain.StageInterview, domain.StageAnalysis, domain.StageSynthesis, domain.StageReview}
	if !equalStages(stepNames(steps), want) {
		t.Fatalf("steps = %v, want %v", stepNames(steps), want)
	}
	analysis, ok := steps[1].(*AgentAnalysisStep)
	if !ok {
		t.Fatalf("analysis step = %T", steps[1])
	}
	if analysis.Client != client || analysis.PromptVersion != "v2" {
		t.Fatalf("analysis step wiring = %+v", analysis)
	}
}

func TestNewBatchPlanAIWhenClientPresent(t *testing.T) {
	store := newFakeAITreeStore()
	plan := NewBatchPlan(BatchOptions{
		Analyst:              &fakeAnalystClient{},
		AnalystPromptVersion: "v2",
		Synthesizer:          newRealSynthesizer(t),
		Store:                store,
		VacancyTopK:          5,
	})
	if !plan.HasAI() {
		t.Fatal("expected the AI chain when the analyst client is configured")
	}
	if plan.AITimeout != DefaultAIStepTimeout {
		t.Fatalf("AI timeout = %s, want %s", plan.AITimeout, DefaultAIStepTimeout)
	}
	if plan.FallbackTimeout != DefaultVacancyStepTimeout {
		t.Fatalf("fallback timeout = %s, want %s", plan.FallbackTimeout, DefaultVacancyStepTimeout)
	}
	if plan.MaxRetries != DefaultBatchMaxRetries {
		t.Fatalf("max retries = %d, want %d", plan.MaxRetries, DefaultBatchMaxRetries)
	}

	aiSteps, timeout := plan.For(true)
	if !equalStages(stepNames(aiSteps), []domain.Stage{domain.StageInterview, domain.StageAnalysis, domain.StageSynthesis, domain.StageReview}) {
		t.Fatalf("AI steps = %v", stepNames(aiSteps))
	}
	if timeout != DefaultAIStepTimeout {
		t.Fatalf("timeout = %s", timeout)
	}

	fallbackSteps, fallbackTimeout := plan.For(false)
	if !equalStages(stepNames(fallbackSteps), []domain.Stage{domain.StageInterview, domain.StageAnalysis, domain.StageSynthesis, domain.StageReview}) {
		t.Fatalf("fallback steps = %v", stepNames(fallbackSteps))
	}
	if fallbackTimeout != DefaultVacancyStepTimeout {
		t.Fatalf("fallback timeout = %s", fallbackTimeout)
	}
	if _, ok := fallbackSteps[1].(*AgentAnalysisStep); ok {
		t.Fatal("fallback chain must not contain the AI analyst step")
	}
	if _, ok := fallbackSteps[1].(*AnalysisStep); !ok {
		t.Fatalf("fallback analysis step = %T", fallbackSteps[1])
	}

	// The fallback keeps the vacancy review store wired.
	review, ok := fallbackSteps[3].(*ReviewStep)
	if !ok || review.Store == nil {
		t.Fatalf("fallback review = %+v", fallbackSteps[3])
	}
}

func TestNewBatchPlanFallsBackWithoutAnalyst(t *testing.T) {
	plan := NewBatchPlan(BatchOptions{Store: newFakeAITreeStore(), VacancyTopK: 5})
	if plan.HasAI() {
		t.Fatal("AI chain must stay disabled without an analyst client")
	}
	steps, timeout := plan.For(true)
	if !equalStages(stepNames(steps), []domain.Stage{domain.StageInterview, domain.StageAnalysis, domain.StageSynthesis, domain.StageReview}) {
		t.Fatalf("steps = %v", stepNames(steps))
	}
	if timeout != DefaultVacancyStepTimeout {
		t.Fatalf("timeout = %s, want %s", timeout, DefaultVacancyStepTimeout)
	}

	// An analyst without a synthesizer still cannot run the AI chain.
	plan = NewBatchPlan(BatchOptions{Analyst: &fakeAnalystClient{}, Store: newFakeAITreeStore()})
	if plan.HasAI() {
		t.Fatal("AI chain must stay disabled without a synthesizer")
	}
}

func TestAgentAnalysisStepUsesPinnedPromptVersion(t *testing.T) {
	run := &domain.Run{ID: "run-prompt", Profession: "Аналитик"}
	SetTranscript(run, "Я аналитик данных и готовлю отчёты.", "")
	step := &AgentAnalysisStep{Client: &fakeAnalystClient{}, PromptVersion: "version-that-does-not-exist"}
	if err := step.ExecuteWithData(context.Background(), run, nil); err == nil {
		t.Fatal("unknown prompt version must fail")
	}
}

func TestBatchOrchestratorProducesAndPersistsTree(t *testing.T) {
	client := &fakeAnalystClient{}
	store := newFakeAITreeStore()
	plan := NewBatchPlan(BatchOptions{
		Analyst:     client,
		Synthesizer: newRealSynthesizer(t),
		Store:       store,
		VacancyTopK: 10,
	})
	run := &domain.Run{
		ID:         "run-batch-ai",
		Status:     domain.RunPending,
		Stage:      domain.StageCreated,
		Profession: "Аналитик данных",
	}
	SetTranscript(run, "Я аналитик данных и готовлю отчёты.", "")

	runSteps, timeout := plan.For(true)
	if err := domain.Orchestrate(context.Background(), run, runSteps, store, fakeVacancyReader{empty: true}, plan.MaxRetries, timeout); err != nil {
		t.Fatalf("orchestrate: %v", err)
	}

	if run.Stage != domain.StageDone || run.Status != domain.RunDone {
		t.Fatalf("run = stage %s status %s", run.Stage, run.Status)
	}
	saved := store.Saved(run.ID)
	if len(saved) == 0 {
		t.Fatal("AI batch path did not persist a tree")
	}
	if err := domain.ValidateTree(saved); err != nil {
		t.Fatalf("persisted tree invalid: %v", err)
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
	sourceID, _ := run.Data[DataKeyInterviewSourceID].(string)
	if sourceID == "" {
		t.Fatal("interview source id missing")
	}
	for _, node := range saved {
		for _, evidence := range node.Evidence {
			if evidence.SourceID != sourceID {
				t.Fatalf("evidence source = %q, want %q", evidence.SourceID, sourceID)
			}
		}
	}
	if store.created != 1 {
		t.Fatalf("created interview sources = %d, want 1", store.created)
	}
	if client.CallCount() != 1 {
		t.Fatalf("analyst calls = %d, want 1", client.CallCount())
	}
}

func TestBatchOrchestratorRetriesFailedStep(t *testing.T) {
	client := &fakeAnalystClient{err: errors.New("provider down")}
	store := newFakeAITreeStore()
	plan := NewBatchPlan(BatchOptions{
		Analyst:     client,
		Synthesizer: newRealSynthesizer(t),
		Store:       store,
		MaxRetries:  DefaultBatchMaxRetries,
	})
	run := &domain.Run{ID: "run-batch-fail", Status: domain.RunPending, Stage: domain.StageCreated, Profession: "Аналитик"}
	SetTranscript(run, "Я аналитик данных и готовлю отчёты.", "")

	runSteps, timeout := plan.For(true)
	err := domain.Orchestrate(context.Background(), run, runSteps, store, fakeVacancyReader{}, plan.MaxRetries, timeout)
	if err == nil {
		t.Fatal("expected orchestration error")
	}
	if run.Stage != domain.StageFailed || run.Status != domain.RunFailed {
		t.Fatalf("run = stage %s status %s", run.Stage, run.Status)
	}
	if got, want := client.CallCount(), plan.MaxRetries+1; got != want {
		t.Fatalf("analyst calls = %d, want %d", got, want)
	}
	if saved := store.Saved(run.ID); len(saved) != 0 {
		t.Fatalf("failed run must not persist a tree: %+v", saved)
	}
}

func TestBatchOrchestratorTimesOutStep(t *testing.T) {
	client := &fakeAnalystClient{block: true}
	store := newFakeAITreeStore()
	plan := NewBatchPlan(BatchOptions{
		Analyst:     client,
		Synthesizer: newRealSynthesizer(t),
		Store:       store,
		AITimeout:   20 * time.Millisecond,
		MaxRetries:  DefaultBatchMaxRetries,
	})
	run := &domain.Run{ID: "run-batch-timeout", Status: domain.RunPending, Stage: domain.StageCreated, Profession: "Аналитик"}
	SetTranscript(run, "Я аналитик данных и готовлю отчёты.", "")

	runSteps, timeout := plan.For(true)
	if timeout != 20*time.Millisecond {
		t.Fatalf("timeout = %s, want 20ms", timeout)
	}
	err := domain.Orchestrate(context.Background(), run, runSteps, store, fakeVacancyReader{}, plan.MaxRetries, timeout)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if run.Stage != domain.StageFailed || run.Status != domain.RunFailed {
		t.Fatalf("run = stage %s status %s", run.Stage, run.Status)
	}
	if got, want := client.CallCount(), plan.MaxRetries+1; got != want {
		t.Fatalf("analyst calls = %d, want %d", got, want)
	}
}

func TestBatchOrchestratorUsesVacancyFallbackWithoutTranscript(t *testing.T) {
	client := &fakeAnalystClient{}
	store := newFakeAITreeStore()
	plan := NewBatchPlan(BatchOptions{
		Analyst:     client,
		Synthesizer: newRealSynthesizer(t),
		Store:       store,
		VacancyTopK: 10,
	})
	run := &domain.Run{ID: "run-batch-vacancy", Status: domain.RunPending, Stage: domain.StageCreated, Profession: "Go Developer"}

	runSteps, timeout := plan.For(false)
	if err := domain.Orchestrate(context.Background(), run, runSteps, store, fakeVacancyReader{}, plan.MaxRetries, timeout); err != nil {
		t.Fatalf("orchestrate: %v", err)
	}
	if run.Stage != domain.StageDone || run.Status != domain.RunDone {
		t.Fatalf("run = stage %s status %s", run.Stage, run.Status)
	}
	if client.CallCount() != 0 {
		t.Fatalf("vacancy fallback must not call the analyst, calls = %d", client.CallCount())
	}
	if saved := store.Saved(run.ID); len(saved) == 0 {
		t.Fatal("vacancy fallback did not persist a tree")
	}
}

func TestSaveAITreeStepSkipsExistingTree(t *testing.T) {
	run := &domain.Run{ID: "run-existing-tree", Data: map[string]any{
		DataKeyTranscript: "готовлю отчёты",
		DataKeyTree: []domain.Node{{
			ID: "n1", Type: domain.NodeRole, Title: "Роль",
			Evidence: []domain.Evidence{{ID: "e1", SourceID: "x", Quote: "q"}},
		}},
	}}
	store := newFakeAITreeStore()
	store.treeExists = true
	if err := (&SaveAITreeStep{Store: store, NewID: sequenceUUID()}).Execute(context.Background(), run); err != nil {
		t.Fatalf("existing tree must be tolerated on resume: %v", err)
	}
	if store.created != 0 || len(store.Saved(run.ID)) != 0 {
		t.Fatal("existing tree must not be overwritten or duplicated")
	}
}

func TestPersistAndLoadInterviewTranscript(t *testing.T) {
	store := newFakeAITreeStore()
	transcript := "Я аналитик данных и готовлю отчёты."
	sourceID, err := PersistInterviewTranscript(context.Background(), store, "run-transcript", transcript)
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	if sourceID == "" {
		t.Fatal("empty source id")
	}
	if len(store.links["run-transcript"]) != 1 || store.links["run-transcript"][0] != sourceID {
		t.Fatalf("links = %+v", store.links)
	}

	gotTranscript, gotSourceID, found, err := LoadInterviewTranscript(context.Background(), store, "run-transcript")
	if err != nil || !found {
		t.Fatalf("load: found=%v err=%v", found, err)
	}
	if gotTranscript != transcript || gotSourceID != sourceID {
		t.Fatalf("loaded %q / %q, want %q / %q", gotTranscript, gotSourceID, transcript, sourceID)
	}

	if _, _, found, err := LoadInterviewTranscript(context.Background(), store, "run-missing"); err != nil || found {
		t.Fatalf("missing run: found=%v err=%v", found, err)
	}
}

func TestLoadInterviewTranscriptIgnoresOtherSources(t *testing.T) {
	store := newFakeAITreeStore()
	store.sources["vacancy-source"] = fakeSource{runID: "run-other", transcript: "go", sourceType: domain.SourceVacancy}
	store.links["run-other"] = []string{"vacancy-source"}
	store.sources["empty-interview"] = fakeSource{runID: "run-other", transcript: "  ", sourceType: domain.SourceInterview}
	store.links["run-other"] = append(store.links["run-other"], "empty-interview")

	if _, _, found, err := LoadInterviewTranscript(context.Background(), store, "run-other"); err != nil || found {
		t.Fatalf("non-interview sources must not be treated as a transcript: found=%v err=%v", found, err)
	}
}

func TestPersistInterviewTranscriptErrors(t *testing.T) {
	if _, err := PersistInterviewTranscript(context.Background(), nil, "run", "text"); err == nil {
		t.Fatal("nil store must fail")
	}
	if _, err := PersistInterviewTranscript(context.Background(), newFakeAITreeStore(), "run", "   "); err == nil {
		t.Fatal("empty transcript must fail")
	}
	createErrStore := newFakeAITreeStore()
	createErrStore.createErr = errors.New("create failed")
	if _, err := PersistInterviewTranscript(context.Background(), createErrStore, "run", "text"); err == nil {
		t.Fatal("create error must propagate")
	}
	linkErrStore := newFakeAITreeStore()
	linkErrStore.linkErr = errors.New("link failed")
	if _, err := PersistInterviewTranscript(context.Background(), linkErrStore, "run", "text"); err == nil {
		t.Fatal("link error must propagate")
	}
}
