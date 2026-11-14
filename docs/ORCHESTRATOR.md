# Оркестратор и как подключить к нему AI-агентов

## Что такое оркестратор

Оркестратор — это диспетчер этапов исследования. Он не задаёт вопросы и не
анализирует текст сам. Он:

1. берёт run;
2. последовательно вызывает шаги: interview, analysis, synthesis, review;
3. после каждого шага сохраняет стадию и статус;
4. повторяет шаг при ошибке;
5. отправляет SSE-события;
6. переводит run в done или failed.

Код: `internal/domain/orchestrator.go`.

Интерфейс шага:

```go
type Step interface {
    Name() Stage
    Execute(ctx context.Context, run *Run) error
}

type DataStep interface {
    Step
    ExecuteWithData(ctx context.Context, run *Run, vacancies []VacancyRecord) error
}
```

`DataStep` нужен, когда шагу требуются вакансии из БД.

## Что уже есть в backend

- `InterviewStep` — заглушка, ничего не делает;
- `AnalysisStep` — анализирует вакансии правилами;
- `SynthesisStep` — считает уровни по вакансиям;
- `ReviewStep` — строит дерево и сохраняет его.

При создании run backend запускает все шаги в фоне и передаёт SSE-события.

## В чём проблема

Для чат-интервью оркестратор не подходит в текущем виде:

- `InterviewStep` не может ждать, пока человек отвечает;
- анализ должен идти по стенограмме интервью, а не по вакансиям;
- синтез должен собрать дерево из интервью, а не из уровней вакансий.

Сейчас чат и финализация живут отдельно от оркестратора: после шестого ответа
`chat.Service` сам вызывает аналитика и синтезатора.

## Как соединить правильно

### 1. Создание run

Добавить в `POST /runs` тип:

```json
{"profession":"Аналитик данных","type":"chat"}
```

Для `batch` оставить текущий оркестратор. Для `chat`:

- создать run с `type=chat`;
- не запускать batch-шаги автоматически;
- перевести стадию в `interview`.

### 2. Интервью

Чат-сервис задаёт вопросы и собирает стенограмму. Когда интервью закончено:

```go
run.Data = map[string]any{"transcript": transcript}
run.Stage = domain.StageInterview
run.Status = domain.RunRunning
```

### 3. Продолжение оркестратора

Запустить оркестратор не с начала, а с анализа:

```go
steps := []domain.Step{
    &steps.AgentAnalysisStep{Client: analystClient},
    &steps.AgentSynthesisStep{},
    &steps.SaveAITreeStep{Store: treeStore},
}

err := domain.Orchestrate(ctx, run, steps, eventStore, nil, 2, 2*time.Minute)
```

Оркестратор увидит, что run уже на стадии `interview`, пропустит interview и
выполнит analysis, synthesis, review. После review он сам поставит `done`.

### 4. Что делает каждый AI-шаг

- `AgentAnalysisStep` читает `run.Data["transcript"]`, вызывает аналитика,
  кладёт узлы в `run.Data["interview_nodes"]`.
- `AgentSynthesisStep` читает `interview_nodes`, вызывает синтезатора,
  кладёт дерево и конфликты в `run.Data["tree"]` и `["conflicts"]`.
- `SaveAITreeStep` сохраняет дерево через существующий `TreeStorer`.

Эти шаги уже добавлены в `internal/steps/ai_steps.go` и покрыты тестами.

## Что нужно от backend

1. Добавить `type` в запрос создания run.
2. Для `chat` не запускать batch-оркестратор сразу.
3. После завершения чата вызвать оркестратор с AI-шагами.
4. Убедиться, что `SaveAITreeStep` получает PostgreSQL-store.
5. Не включать чат-прогоны в resume, пока стенограмма не сохраняется в БД.

## Что важно для resume

Сейчас `resume` пропускает `runs.type=chat`. Это правильно: после рестарта
стенограмма чата потеряна, и продолжить анализ нечем. Если нужно
восстанавливать чат-прогоны, сначала нужно сохранять сообщения и стенограмму
в БД.

## Итоговая схема для чата

```text
POST /runs type=chat
        |
        v
chat: вопросы и ответы
        |
        v
transcript в run.Data, stage=interview
        |
        v
orchestrator: analysis -> synthesis -> review -> done
        |
        v
SSE: stage_change, done
```

Batch-прогоны остаются на старой схеме: interview-заглушка, анализ вакансий,
синтез уровней, review.
