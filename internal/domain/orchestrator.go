package domain

import (
	"context"
	"fmt"
	"time"
)

// Step — розетка «один шаг процесса». За ней потом стоят агенты R (15-17)
// вместе с валидатором (13). Оркестратору важно только имя стадии и то,
// упал шаг или нет.
type Step interface {
	Name() Stage
	Execute(ctx context.Context, run *Run) error
}

// DataStep — шаг, которому нужны реальные вакансии перед исполнением.
// Оркестратор дёргает reader.ByProfession(run.Profession) и передаёт результат
// в ExecuteWithData. Если шаг не реализует DataStep — вызывается обычный Execute.
type DataStep interface {
	Step
	ExecuteWithData(ctx context.Context, run *Run, vacancies []VacancyRecord) error
}

// Store — розетка «журнал прогонов». За ней in-memory (для теста) или
// реальный store от S (4-5). Оркестратор не знает, где физически лежит база.
type Store interface {
	Save(run *Run) error
}

// Orchestrate ведёт один прогон по цепочке шагов.
//   - ctx        — общий контекст запуска (отмена «сверху»)
//   - run        — прогон; на входе ожидается StageCreated
//   - steps      — цепочка шагов по порядку (interview, analysis, ...)
//   - store      — журнал, куда пишем каждое движение стадии
//   - reader     — доступ к реальным вакансиям (nil = шаги работают без данных)
//   - maxRetries — сколько раз ПОВТОРИТЬ упавший шаг (0 = без повторов)
//   - stepTO     — таймаут на один шаг (висящий шаг убивается по нему)
//
// Правила: перескок стадии запрещён таблицей CanTransition; упавший шаг
// ретраится; после исчерпания повторов или таймаута прогон уходит в failed;
// повторный запуск уже готового/упавшего прогона ничего не трогает
// (идемпотентность — «двойной тычок = один тычок»).
//
// Связь полей (согласовано с триггером БД): status — грубая лампочка,
// меняется только в трёх точках — вход (pending->running), успех (->done),
// сбой (->failed). Промежуточные стадии status не трогают намеренно, иначе
// триггер спамил бы событиями на каждом шаге; по-этапные события идут через
// SSE (карточка 20), а не через мутацию status.
func Orchestrate(
	ctx context.Context,
	run *Run,
	steps []Step,
	store Store,
	reader VacancyReader,
	maxRetries int,
	stepTO time.Duration,
) error {
	// Идемпотентность: если прогон уже в терминальной стадии — выходим,
	// не дёргая шаги повторно. Это защита от двойного запуска.
	if run.Stage == StageDone || run.Stage == StageFailed {
		return nil
	}

	// Вход в работу: pending -> running один раз. Промежуточные стадии
	// status не трогают (running держится весь активный этап).
	if run.Status == RunPending {
		run.Status = RunRunning
		if err := store.Save(run); err != nil {
			return fail(run, store, fmt.Errorf("save on start: %w", err))
		}
	}

	for _, st := range steps {
		target := st.Name()

		// Не даём перескакивать, даже если шаг «готов» сработать.
		if !CanTransition(run.Stage, target) {
			return fail(run, store, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, run.Stage, target))
		}

		// Retry + таймаут на один шаг.
		var lastErr error
		for attempt := 0; attempt <= maxRetries; attempt++ {
			stepCtx, cancel := context.WithTimeout(ctx, stepTO)

			// Если шаг нуждается в данных — читаем вакансии через reader.
			if ds, ok := st.(DataStep); ok && reader != nil {
				vacs, err := reader.ByProfession(stepCtx, run.Profession, 100)
				if err != nil {
					lastErr = fmt.Errorf("read vacancies: %w", err)
				} else {
					lastErr = ds.ExecuteWithData(stepCtx, run, vacs)
				}
			} else {
				lastErr = st.Execute(stepCtx, run)
			}

			cancel()
			if lastErr == nil {
				break
			}
		}
		if lastErr != nil {
			return fail(run, store, fmt.Errorf("step %s failed after %d retries: %w", target, maxRetries, lastErr))
		}

		// Шаг прошёл — двигаем стадию и пишем в журнал.
		run.Stage = target
		if err := store.Save(run); err != nil {
			return fail(run, store, fmt.Errorf("save after %s: %w", target, err))
		}
	}

	// Все шаги пройдены — финальный переход в done (тоже через таблицу).
	if !CanTransition(run.Stage, StageDone) {
		return fail(run, store, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, run.Stage, StageDone))
	}
	run.Stage = StageDone
	run.Status = RunDone
	return store.Save(run)
}

// fail — общий хвост: помечаем прогон упавшим (и стадией, и статусом),
// пишем в журнал, возвращаем причину. Вынесено, чтобы не дублировать блоки.
func fail(run *Run, store Store, reason error) error {
	run.Stage = StageFailed
	run.Status = RunFailed
	_ = store.Save(run)
	return reason
}
