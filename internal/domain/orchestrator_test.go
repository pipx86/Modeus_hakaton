package domain

import (
	"context"
	"testing"
	"time"
)

type memStore struct{ runs map[string]*Run }

func (m *memStore) Save(r *Run) error {
	if m.runs == nil {
		m.runs = map[string]*Run{}
	}
	m.runs[r.ID] = r
	return nil
}

type fakeStep struct {
	stage     Stage
	failTimes int
	calls     int
}

func (f *fakeStep) Name() Stage { return f.stage }
func (f *fakeStep) Execute(_ context.Context, _ *Run) error {
	f.calls++
	if f.calls <= f.failTimes {
		return ErrInvalidTransition
	}
	return nil
}

type hangStep struct{ stage Stage }

func (h hangStep) Name() Stage { return h.stage }
func (h hangStep) Execute(ctx context.Context, _ *Run) error {
	<-ctx.Done()
	return ctx.Err()
}

func newRun() *Run {
	return &Run{ID: "r1", Status: RunRunning, Profession: "cook", Stage: StageCreated}
}

func TestOrchestrateHappy(t *testing.T) {
	steps := []Step{
		&fakeStep{stage: StageInterview},
		&fakeStep{stage: StageAnalysis},
		&fakeStep{stage: StageSynthesis},
		&fakeStep{stage: StageReview},
	}
	run := newRun()
	store := &memStore{}
	if err := Orchestrate(context.Background(), run, steps, store, 2, time.Second); err != nil {
		t.Fatalf("happy path не должен падать: %v", err)
	}
	if run.Stage != StageDone {
		t.Fatalf("ожидали done, получили %s", run.Stage)
	}
	if store.runs["r1"].Stage != StageDone {
		t.Fatalf("журнал не обновился до done")
	}
}

func TestOrchestrateRetry(t *testing.T) {
	bad := &fakeStep{stage: StageInterview, failTimes: 2}
	steps := []Step{bad, &fakeStep{stage: StageAnalysis}, &fakeStep{stage: StageSynthesis}, &fakeStep{stage: StageReview}}
	run := newRun()
	if err := Orchestrate(context.Background(), run, steps, &memStore{}, 2, time.Second); err != nil {
		t.Fatalf("retry должен спасти прогон: %v", err)
	}
	if bad.calls != 3 {
		t.Fatalf("ожидали 3 попытки (2 падения + успех), получили %d", bad.calls)
	}
	if run.Stage != StageDone {
		t.Fatalf("после retry ожидали done, получили %s", run.Stage)
	}
}

func TestOrchestrateTimeout(t *testing.T) {
	steps := []Step{hangStep{stage: StageInterview}}
	run := newRun()
	err := Orchestrate(context.Background(), run, steps, &memStore{}, 1, 20*time.Millisecond)
	if err == nil {
		t.Fatalf("таймаут обязан был породить ошибку")
	}
	if run.Stage != StageFailed {
		t.Fatalf("ожидали failed, получили %s", run.Stage)
	}
}

func TestOrchestrateInvalidTransition(t *testing.T) {
	sneaky := &fakeStep{stage: StageDone}
	run := newRun()
	err := Orchestrate(context.Background(), run, []Step{sneaky}, &memStore{}, 0, time.Second)
	if err == nil {
		t.Fatalf("перескок обязан был быть отклонён")
	}
	if sneaky.calls != 0 {
		t.Fatalf("шаг не должен был дёргаться при запрещённом переходе")
	}
	if run.Stage != StageFailed {
		t.Fatalf("ожидали failed, получили %s", run.Stage)
	}
}

func TestOrchestrateIdempotent(t *testing.T) {
	interview := &fakeStep{stage: StageInterview}
	steps := []Step{interview, &fakeStep{stage: StageAnalysis}, &fakeStep{stage: StageSynthesis}, &fakeStep{stage: StageReview}}
	run := newRun()
	if err := Orchestrate(context.Background(), run, steps, &memStore{}, 0, time.Second); err != nil {
		t.Fatalf("первый прогон упал: %v", err)
	}
	before := interview.calls
	if err := Orchestrate(context.Background(), run, steps, &memStore{}, 0, time.Second); err != nil {
		t.Fatalf("второй прогон упал: %v", err)
	}
	if interview.calls != before {
		t.Fatalf("повторный запуск дёрнул шаги заново: было %d, стало %d", before, interview.calls)
	}
	if run.Stage != StageDone {
		t.Fatalf("повторный запуск сломал done: %s", run.Stage)
	}
}
