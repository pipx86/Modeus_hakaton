package domain

import (
	"context"
	"testing"
	"time"
)

// memStore — пустышка журнала: держит прогон в памяти.
type memStore struct{ runs map[string]*Run }

func (m *memStore) Save(r *Run) error {
	if m.runs == nil {
		m.runs = map[string]*Run{}
	}
	m.runs[r.ID] = r
	return nil
}

// fakeStep — пустышка шага: падает failTimes раз, потом succeeds.
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

// hangStep — пустышка-висяк: не возвращает контроль, пока не прилетит таймаут.
type hangStep struct{ stage Stage }

func (h hangStep) Name() Stage { return h.stage }
func (h hangStep) Execute(ctx context.Context, _ *Run) error {
	<-ctx.Done()
	return ctx.Err()
}

func newRun() *Run {
	return &Run{ID: "r1", Status: RunRunning, Profession: "cook", Stage: StageCreated}
}

// Happy path: цепочка доходит до done.
func TestOrchestrateHappy(t *testing.T) {
	steps := []Step{
		&fakeStep{stage: StageInterview},
		&fakeStep{stage: StageAnalysis},
		&fakeStep{stage: StageSynthesis},
		&fakeStep{stage: StageReview},
	}
	run := newRun()
	store := &memStore{}
	if err := Orchestrate(context.Background(), run, steps, store, nil, 2, time.Second); err != nil {
		t.Fatalf("happy path не должен падать: %v", err)
	}
	if run.Stage != StageDone {
		t.Fatalf("ожидали done, получили %s", run.Stage)
	}
	if store.runs["r1"].Stage != StageDone {
		t.Fatalf("журнал не обновился до done")
	}
}

// Retry: шаг падает 2 раза, maxRetries=2 -> на 3-й попытке успех, прогон жив.
func TestOrchestrateRetry(t *testing.T) {
	bad := &fakeStep{stage: StageInterview, failTimes: 2}
	steps := []Step{bad, &fakeStep{stage: StageAnalysis}, &fakeStep{stage: StageSynthesis}, &fakeStep{stage: StageReview}}
	run := newRun()
	if err := Orchestrate(context.Background(), run, steps, &memStore{}, nil, 2, time.Second); err != nil {
		t.Fatalf("retry должен спасти прогон: %v", err)
	}
	if bad.calls != 3 {
		t.Fatalf("ожидали 3 попытки (2 падения + успех), получили %d", bad.calls)
	}
	if run.Stage != StageDone {
		t.Fatalf("после retry ожидали done, получили %s", run.Stage)
	}
}

// Timeout: висящий шаг убивается по stepTO, ретраи не помогают -> failed.
func TestOrchestrateTimeout(t *testing.T) {
	steps := []Step{hangStep{stage: StageInterview}}
	run := newRun()
	err := Orchestrate(context.Background(), run, steps, &memStore{}, nil, 1, 20*time.Millisecond)
	if err == nil {
		t.Fatalf("таймаут обязан был породить ошибку")
	}
	if run.Stage != StageFailed {
		t.Fatalf("ожидали failed, получили %s", run.Stage)
	}
}

// Invalid transition: перескок через всю цепочку ловится ДО вызова шага.
func TestOrchestrateInvalidTransition(t *testing.T) {
	sneaky := &fakeStep{stage: StageDone}
	run := newRun()
	err := Orchestrate(context.Background(), run, []Step{sneaky}, &memStore{}, nil, 0, time.Second)
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

// Idempotency: повторный запуск готового прогона ничего не трогает.
func TestOrchestrateIdempotent(t *testing.T) {
	interview := &fakeStep{stage: StageInterview}
	steps := []Step{interview, &fakeStep{stage: StageAnalysis}, &fakeStep{stage: StageSynthesis}, &fakeStep{stage: StageReview}}
	run := newRun()
	if err := Orchestrate(context.Background(), run, steps, &memStore{}, nil, 0, time.Second); err != nil {
		t.Fatalf("первый прогон упал: %v", err)
	}
	before := interview.calls
	if err := Orchestrate(context.Background(), run, steps, &memStore{}, nil, 0, time.Second); err != nil {
		t.Fatalf("второй прогон упал: %v", err)
	}
	if interview.calls != before {
		t.Fatalf("повторный запуск дёрнул шаги заново: было %d, стало %d", before, interview.calls)
	}
	if run.Stage != StageDone {
		t.Fatalf("повторный запуск сломал done: %s", run.Stage)
	}
}

// Синхронизация status<->stage: триггер БД следит за runs.status, поэтому
// на успехе обязан быть done, на сбое — failed (иначе run.completed не родится).
func TestOrchestrateSyncsStatusWithStage(t *testing.T) {
	ok := []Step{
		&fakeStep{stage: StageInterview},
		&fakeStep{stage: StageAnalysis},
		&fakeStep{stage: StageSynthesis},
		&fakeStep{stage: StageReview},
	}
	run := &Run{ID: "rs", Status: RunPending, Profession: "cook", Stage: StageCreated}
	if err := Orchestrate(context.Background(), run, ok, &memStore{}, nil, 0, time.Second); err != nil {
		t.Fatalf("успешный прогон упал: %v", err)
	}
	if run.Stage != StageDone || run.Status != RunDone {
		t.Fatalf("успех обязан синхронизировать поля: stage=%s status=%s", run.Stage, run.Status)
	}

	bad := []Step{&fakeStep{stage: StageInterview, failTimes: 99}}
	run2 := &Run{ID: "rf", Status: RunPending, Profession: "cook", Stage: StageCreated}
	if err := Orchestrate(context.Background(), run2, bad, &memStore{}, nil, 0, time.Second); err == nil {
		t.Fatalf("сбой обязан вернуть ошибку")
	}
	if run2.Stage != StageFailed || run2.Status != RunFailed {
		t.Fatalf("сбой обязан синхронизировать поля: stage=%s status=%s", run2.Stage, run2.Status)
	}
}

// dataAwareStep — вспомогательный тип для теста VacancyReader.
type dataAwareStep struct {
	name   Stage
	onExec func([]VacancyRecord)
}

func (d *dataAwareStep) Name() Stage { return d.name }
func (d *dataAwareStep) Execute(_ context.Context, _ *Run) error {
	d.onExec(nil)
	return nil
}
func (d *dataAwareStep) ExecuteWithData(_ context.Context, _ *Run, vacs []VacancyRecord) error {
	d.onExec(vacs)
	return nil
}

// Интеграция VacancyReader: шаг типа DataStep получает реальные вакансии через reader.
// Интеграция VacancyReader: шаг типа DataStep получает реальные вакансии через reader.
func TestOrchestrateWithVacancyReader(t *testing.T) {
	mock := NewMockVacancyReader()
	mock.Add("cook",
		VacancyRecord{ID: "v1", Title: "Senior Cook", Description: "..."},
		VacancyRecord{ID: "v2", Title: "Junior Cook", Description: "..."},
	)

	receivedCount := 0
	interviewStep := &dataAwareStep{name: StageInterview, onExec: func(vacs []VacancyRecord) {
		receivedCount = len(vacs)
	}}

	// Полная цепочка: interview -> analysis -> synthesis -> review -> done.
	steps := []Step{
		interviewStep,
		&fakeStep{stage: StageAnalysis},
		&fakeStep{stage: StageSynthesis},
		&fakeStep{stage: StageReview},
	}

	run := newRun()
	err := Orchestrate(context.Background(), run, steps, &memStore{}, mock, 0, time.Second)
	if err != nil {
		t.Fatalf("прогон упал: %v", err)
	}
	if receivedCount != 2 {
		t.Fatalf("шаг получил %d вакансий, ожидали 2", receivedCount)
	}
	if run.Stage != StageDone {
		t.Fatalf("ожидали done, получили %s", run.Stage)
	}
}
