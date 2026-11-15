# Архитектура пайплайна агентов

Документ описывает pipeline так, как его можно показать наставнику: стадии,
контракты, вызовы LLM, ошибки, прогресс, события, сохранение и мониторинг.

Ориентир по структуре: `Архитектура (2).docx` — слоистый pipeline с
оркестратором, стадиями, прогрессом и сохранением.

Важно: в текущем этапе участвуют **только профессиональные роли** — аналитик
данных, DevOps-инженер, backend-разработчик. Бизнес-роли руководителя,
методиста и оператора не входят в scope.

## 1. Место pipeline в системе

```text
Client
  |
  v
Go API: runs, messages, SSE, auth
  |
  v
Pipeline Orchestrator
  |
  +-- InterviewStage  -> interviewer agent
  +-- AnalysisStage   -> analyst agent
  +-- SynthesisStage  -> synthesizer
  +-- ReviewStage     -> validation + persistence
  |
  v
PostgreSQL: runs, stage data, tree, evidence, LLM audit
  |
  v
SSE, metrics, logs, OTel
```

Слои:

- **API** принимает запросы и отдаёт статусы;
- **Orchestrator** управляет порядком стадий;
- **Agents** вызывают LLM и проверяют результат;
- **Store** сохраняет состояние и результат;
- **Observability** показывает прогресс, метрики и ошибки.

## 2. Жизненный цикл run

```text
created
  |
  v
interview (0..20%)
  |
  v
analysis (20..40%)
  |
  v
synthesis (40..60%)
  |
  v
review (60..80%)
  |
  v
done (100%) / degraded / failed
```

Статусы run:

- `pending` — создан, но не запущен;
- `running` — пайплайн выполняется;
- `done` — результат готов;
- `degraded` — результат готов, но часть шагов работала в fallback;
- `failed` — выполнение остановлено ошибкой.

Типы run:

- `chat` — интервью с человеком;
- `batch` — анализ по сохранённым источникам и вакансиям.

## 3. Два режима

### 3.1 Chat

```text
POST /runs type=chat
  |
  v
InterviewStage
  - создаёт сессию интервью
  - задаёт 6 вопросов
  - принимает ответы
  - собирает transcript
  |
  v
AnalysisStage
  - аналитик извлекает функции, задачи, действия
  - каждая сущность получает evidence
  |
  v
SynthesisStage
  - синтезатор собирает дерево
  - разрешает конфликты источников
  |
  v
ReviewStage
  - проверяет дерево
  - сохраняет nodes, evidence, snapshot
  |
  v
done
```

Особенность: интервью — интерактивная стадия. Оркестратор не может выполнить
её одним синхронным вызовом. Поэтому:

1. `InterviewStage` создаёт сессию и переводит run в `interview`.
2. `POST /runs/{id}/messages` принимает ответ человека.
3. После последнего ответа стенограмма сохраняется в stage artifact.
4. Оркестратор продолжает с `analysis`.

### 3.2 Batch

```text
POST /runs type=batch
  |
  v
InterviewStep (заглушка)
  |
  v
AnalysisStep
  - извлечение навыков из вакансий
  |
  v
SynthesisStep
  - уровни и предварительное дерево
  |
  v
ReviewStep
  - сохранение результата
  |
  v
done
```

Batch остаётся для анализа без человека. Chat и batch используют один
оркестратор, но разные наборы шагов.

## 4. Контракт стадии

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

Требования к стадии:

- одна ответственность;
- детерминированный вход и выход;
- ошибка не глотается;
- результат сохраняется до перехода дальше;
- стадия не меняет чужие данные;
- стадия возвращает typed error.

## 5. Стадии подробно

### 5.1 InterviewStage

**Цель:** получить описание деятельности от носителя профессиональной роли.

Вход:

- run id;
- профессия;
- respondent: имя, роль, уровень;
- план вопросов: role, function, task, action, task, action.

Выход:

- `InterviewSession`;
- список вопросов и ответов;
- transcript;
- флаги: clarification, fallback, degraded.

LLM:

- 6 вызовов интервьюера;
- один вопрос за вызов;
- JSON по `interviewer.output.schema.json`;
- температура 0.4;
- max tokens 1500;
- repair при невалидном JSON;
- fallback-вопрос при ошибке провайдера.

Проверки:

- вопрос не пустой;
- уровень вопроса валиден;
- clarification согласован с флагом;
- JSON проходит схему.

Ошибки:

- провайдер недоступен → fallback-вопрос, run `degraded`;
- невалидный JSON → repair, затем fallback;
- пустой ответ → fallback;
- таймаут → fallback.

Сохранение:

- `run` stage=interview;
- сообщения чата;
- stage artifact с transcript;
- SSE-события.

Прогресс:

- 0% — старт;
- 1/6..6/6 вопросов → 0..20%.

### 5.2 AnalysisStage

**Цель:** извлечь из transcript функции, задачи и действия.

Вход:

- transcript;
- профессия;
- источники, если есть;
- версия таксономии.

Выход:

- `AnalystOutput.entities`;
- узлы с evidence: source_id, ref_id, quote;
- confidence.

LLM:

- 1 вызов аналитика;
- JSON по `analyst.output.schema.json`;
- температура 0.2;
- max tokens 5000;
- repair до 1 раза.

Проверки:

- каждая сущность имеет evidence;
- quote дословно есть в transcript;
- source_id и ref_id заполнены;
- тип узла валиден;
- нет дублей после нормализации;
- PII не попадает в title/description.

Ошибки:

- невалидный JSON → repair;
- цитата не найдена → repair;
- после repair ошибка → run failed;
- провайдер недоступен → degraded или failed по политике.

Сохранение:

- stage artifact `analysis`;
- `llm_requests`/`llm_responses`/`llm_usage`;
- SSE `stage_change`.

Прогресс:

- старт 20%;
- конец 40%.

### 5.3 SynthesisStage

**Цель:** собрать одно дерево из интервью, вакансий и стандартов.

Вход:

- interview nodes;
- vacancy nodes;
- standard nodes;
- sources с приоритетами.

Выход:

- `tree`;
- `conflicts`;
- `messages`.

Логика:

- приоритет: интервью > стандарт > вакансия;
- дубликаты объединяются;
- evidence всех источников сохраняется;
- победитель конфликта определяет title/description;
- узел без evidence запрещён;
- иерархия: role → function → task → action.

LLM:

- сейчас синтезатор детерминированный, без вызова модели;
- prompt version зафиксирована для будущей нормализации;
- это сделано специально: приоритет и evidence не должны зависеть от модели.

Проверки:

- дерево связное;
- один корень;
- нет циклов;
- нет дублей;
- глубина не больше 4;
- каждый узел с evidence.

Ошибки:

- нет узлов → failed;
- нет корня → failed;
- нарушена иерархия → failed;
- конфликт без победителя → failed.

Сохранение:

- stage artifact `synthesis`;
- conflicts;
- SSE `stage_change`.

Прогресс:

- старт 40%;
- конец 60%.

### 5.4 ReviewStage

**Цель:** проверить и сохранить итоговое дерево.

Вход:

- tree;
- conflicts.

Выход:

- nodes;
- evidence;
- snapshot;
- root_node_id.

Проверки:

- `domain.ValidateTree`;
- constraint'ы БД;
- evidence source существует;
- run существует;
- дерево ещё не сохранено.

Сохранение:

- атомарная транзакция;
- nodes;
- evidence;
- snapshot;
- run.root_node_id;
- run stage=review.

Ошибки:

- нарушение constraint → 422/500;
- повторное сохранение → conflict;
- нет evidence → failed.

Прогресс:

- старт 60%;
- конец 80%.

### 5.5 Done

- run.stage=done;
- run.status=done или degraded;
- finished_at;
- SSE `run.completed`;
- метрики финального результата.

## 6. Оркестратор

```text
for stage in stages:
    if stage already passed: continue
    check transition
    for attempt in 0..maxRetries:
        run stage with timeout
        if success: break
        if not retryable: fail
    save stage/status
    publish event
finalize run
```

Свойства:

- последовательность стадий фиксирована;
- повторный запуск после рестарта продолжает с текущей стадии;
- если стадия уже пройдена — пропускается;
- ошибка стадии сохраняется;
- run не зависает: timeout на стадию;
- события отправляются после сохранения состояния.

## 7. Retry, fallback, degraded

| Ошибка | Поведение |
|---|---|
| 429 rate limit | retry с backoff |
| 5xx / network | retry, затем degraded/failed |
| timeout | retry, затем degraded/failed |
| 401/403 auth | без retry, failed |
| invalid JSON | repair |
| citation mismatch | repair |
| guardrail block | failed, без retry |
| пустой ответ | repair/fallback |

Интервьюер:

- при ошибке модели задаёт fallback-вопрос;
- run становится degraded, но интервью продолжается.

Аналитик:

- fallback-ответа нет;
- после repair ошибка означает failed.

Синтезатор:

- детерминированный;
- ошибка означает failed.

## 8. События SSE

События:

- `run.created`;
- `stage_change` — stage, status, pct, message;
- `agent_thinking` — агент начал работу;
- `agent_message` — сообщение агента;
- `run_status` — done/degraded/failed;
- `run.completed`;
- `run.failed`;
- `ping`.

Требования:

- событие содержит run_id;
- есть монотонный id;
- поддержка Last-Event-ID;
- при реконнекте клиент получает пропущенные события;
- события хранятся в PostgreSQL.

## 9. Прогресс

| Стадия | Диапазон |
|---|---:|
| created | 0% |
| interview | 0–20% |
| analysis | 20–40% |
| synthesis | 40–60% |
| review | 60–80% |
| done | 100% |
| failed | 100% |

Внутри интервью прогресс можно считать по числу вопросов:
`20 * answered / total`.

## 10. Сохранение и восстановление

Что нужно сохранять:

- run: stage, status, type, degraded_reason;
- chat messages;
- transcript;
- stage artifacts (analysis/synthesis);
- LLM requests/responses/usage;
- nodes/evidence/snapshots;
- SSE events;
- prompt versions.

Для resume:

- batch-run можно продолжить с последней стадии;
- chat-run нельзя продолжить без transcript;
- поэтому chat-runs либо не берём в resume, либо сначала сохраняем transcript.

Предлагаемая таблица stage artifacts:

```text
run_stage_data
  run_id UUID
  stage TEXT
  data JSONB
  created_at TIMESTAMPTZ
  UNIQUE (run_id, stage)
```

Она позволит восстановить анализ и синтез после рестарта.

## 11. Мониторинг

Метрики:

- `pipeline_runs_total{type,status}`;
- `pipeline_stage_duration_seconds{stage}`;
- `pipeline_stage_errors_total{stage,code}`;
- `agent_calls_total{agent,model,status,code}`;
- `agent_tokens_total{agent,direction}`;
- `agent_latency_seconds{agent}`;
- `llm_fallback_total{agent}`;
- `llm_degraded_total{agent}`.

Логи:

- `run_id`;
- `stage`;
- `agent`;
- `model`;
- `duration_ms`;
- `status`;
- `error_code`.

Трассировка:

- span на run;
- span на стадию;
- span на LLM-вызов;
- атрибуты: agent, model, tokens, retry.

## 12. Безопасность

- guardrails на входе и выходе LLM;
- PII-маскирование;
- защита от prompt injection;
- защита от jailbreak;
- canary в системном промпте;
- secrets только в env/secret store;
- auth и роли только технические;
- в логах нет промптов и PII.

## 13. Что уже есть и что осталось

Уже есть:

- `domain.Orchestrate`;
- стадии interview/analysis/synthesis/review;
- SSE;
- chat;
- финализация;
- AI-шаги для оркестратора;
- метрики агентов;
- LLM-аудит в БД.

Осталось:

1. Разделить chat и batch на уровне оркестратора.
2. Сохранять chat messages и transcript в БД.
3. Добавить stage artifacts.
4. Единый формат progress и stage events.
5. Подключить stage-метрики в Prometheus/Grafana.
6. Добавить OTel-спаны.
7. Прогнать полный e2e.

Код этих пунктов меняет общие файлы backend, поэтому делаем после мержа.
