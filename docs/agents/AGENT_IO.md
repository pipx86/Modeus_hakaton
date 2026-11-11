# Agent I/O contract `agent.io/v1`

Контракт описывает обмен данными между оркестратором и тремя агентами
(interviewer, analyst, synthesizer). Машинно-читаемые схемы лежат в
`internal/agent/schema/`, Go-зеркала — в `internal/agent/contract.go`, версия
контракта — `agent.ContractVersion = "1.0.0"`.

## Общие инварианты

1. Агент возвращает **только JSON** по схеме своего выхода. Markdown,
   комментарии и текст вокруг JSON запрещены.
2. Любая сущность модели деятельности подтверждается evidence: `source_id`,
   `ref_id` и дословная `quote`. Узел без evidence отклоняется
   (`domain.ErrEvidenceRequired`).
3. Ответ модели проходит валидацию схемой (`internal/llm/validate`) и
   ремонтируется (`internal/llm/repair`, по умолчанию 2 попытки). После
   исчерпания попыток run помечается `failed` с явной ошибкой.
4. Недоступность модели не должна ломать диалог: interviewer переходит на
   детерминированные вопросы, run получает статус `degraded`, а не зависает.
5. Prompt version (`prompt_hash`) и `trace_id` сохраняются вместе с запросом и
   ответом (см. `docs/db/LLM_DATA_MODEL.md`).

## Interviewer

**Вход** (`agent.InterviewerInput`)

| Поле | Тип | Обязательно | Смысл |
|---|---|---|---|
| `session_id` | string | да | идентификатор сессии |
| `respondent` | Respondent | да | кто отвечает (роль, уровень) |
| `profession` | string | нет | целевая профессия |
| `asked` | Question[] | нет | уже заданные вопросы |
| `answers` | Answer[] | нет | ответы в порядке задавания |
| `next_level` | role/function/task/action | да | рекомендуемый уровень вопроса |
| `need_clarification` | bool | нет | последний ответ неконкретен |
| `max_questions` | int | нет | лимит вопросов в сессии |

**Выход** (`agent.InterviewerOutput`): `question` (id, level, text,
rationale, parent_id?, clarification, fallback), `rationale`,
`is_clarification`.

**Инварианты**

- ровно один вопрос за ход; `question.level` — валидный уровень;
- при `need_clarification = true` вопрос уточняющий и
  `question.clarification = is_clarification = true`;
- вопросы не повторяют `asked` (семантически, не только дословно);
- при ошибке модели сессия использует `interviewer.FallbackQuestion` и
  помечает вопрос `fallback = true`, `Session.Status = degraded`.

## Analyst

**Вход** (`agent.AnalystInput`): `session_id`, `profession`, `transcript`,
`sources[]` (вакансия/стандарт), `taxonomy_version`.

**Выход** (`agent.AnalystOutput`): `entities[]`, каждая сущность — узел дерева
(`id`, `type`, `title`, `description?`, `parent_id?`, `confidence?`) плюс
`evidence[]`.

**Инварианты**

- минимум одна evidence на сущность; все поля evidence обязательны;
- `quote` — дословный фрагмент `transcript` (проверяется
  `validate.ContainsQuote` после нормализации пробелов); ответ с недословной
  цитатой отправляется на ремонт, после ремонта — terminal error;
- дубликаты объединяются по нормализованному `(parent_id, title)` с
  объединением evidence (`analyst.DedupEntities`);
- сущность связывается с таксономией по `id`/alias; недостающие предки
  добавляются с агрегированным evidence (`analyst.Taxonomy.Link`).

## Synthesizer

**Вход** (`agent.SynthesizerInput`): `profession`, `sources[]`, `interview[]`,
`vacancy[]`, `standard[]`.

**Выход** (`agent.SynthesizerOutput`): `tree[]`, `conflicts[]`,
`messages[]` (память агента; конфликты дублируются в `messages` с
`kind = "conflict"` и payload).

**Инварианты**

- дерево ровно одной роли: role → function → task → action (action может
  вкладываться в action);
- ни одного узла без evidence; сгенерированный родитель агрегирует evidence
  детей;
- при расхождении `title`/`description` одного `node_id` побеждает источник с
  большим приоритетом: interview (300) > standard (200) > vacancy (100) >
  document (50); проигравшее значение сохраняется в `conflicts`, evidence
  обоих источников остаётся у узла;
- выход валлидируется схемой `synthesizer.output.schema.json` до сохранения.

## Ошибки и деградация

| Ситуация | Поведение | Статус run |
|---|---|---|
| Модель вернула невалидный JSON | repair-попытки с описанием ошибки | `running` → `failed` после лимита |
| HTTP 5xx / таймаут / circuit open | interviewer fallback-вопросы | `degraded` |
| Сущность без evidence/цитаты | repair, затем terminal error | `failed` |
| Конфликт источников | разрешается детерминированно | `done` |

## Метрики контракта

- `contract_validation_failures_total{agent}` — доля ответов, не прошедших
  схему (цель: < 5% после ремонта);
- `repair_attempts_total{agent}` и `repair_exhausted_total{agent}`;
- `evidence_quote_mismatch_total{agent}` — цитаты, не найденные в источнике
  (цель: 0 в принятом результате);
- `degraded_runs_total` — доля run, завершившихся в `degraded` (цель: < 1% в
  норме, 100% при недоступном провайдере без падения).
