# AI-слой на backend

Ветка `feature/ai-on-backend` — перенос AI-слоя из `feature/integration` на
актуальный `feature/Mikrozaym/BackEnd` (`87475f9`). Поведение существующего
backend не менялось: правки в `internal/domain/types.go` и
`internal/llm/schema/*` аддитивные.

## Что добавлено

| Компонент | Что внутри |
|---|---|
| `internal/agent/` | контракты, JSON-схемы, interviewer / analyst / synthesizer, фикстуры диалогов |
| `prompts/`, `internal/prompts/` | системные промпты v1, embed, render, file registry |
| `internal/llm/` | ChatClient, Ollama/OpenRouter-клиенты, validate, repair, circuit breaker, guarded client |
| `internal/llm/agents/` | Nemotron-клиенты на трёх агентов, общий параллельный шлюз, rate limit, retry |
| `internal/guardrails/` | PII, prompt injection, jailbreak |
| `internal/chat/` | сессия интервью, история сообщений, SSE-события |
| `internal/pipeline/` | offline-конвейер интервью, анализ, синтез |
| `cmd/e2e` | офлайн-демо на fake-модели |
| `cmd/nemotron-smoke` | проверка ключа и трёх параллельных вызовов Nemotron |
| `docs/` | AGENT_IO, HYPOTHESES, LLM_DATA_MODEL, NEMOTRON, E2E, LLM_GUARDRAILS |

## Что НЕ переносили (уже есть в backend)

- `internal/api`, `internal/auth`, `internal/store`, `internal/outbox`,
  `internal/webhook` — используем версии backend;
- миграции, compose, CI, Dockerfile — не трогали;
- `internal/domain` — только аддитивные типы (`Respondent`, `Question`,
  `InterviewSession`, `Conflict`, `Evidence.RefID`, `Source.Priority`,
  `RunDegraded`, `NodeType.Valid`).

## Проверки

```powershell
$env:GOCACHE='C:\MyPrograms\CompetencyGapAnalyzer\.worktrees\.gocache'
$env:GOTMPDIR='C:\MyPrograms\CompetencyGapAnalyzer\.worktrees\.gotmp'
$env:GOMODCACHE='C:\MyPrograms\CompetencyGapAnalyzer\.worktrees\.gomodcache'
$env:GOPATH='C:\MyPrograms\CompetencyGapAnalyzer\.worktrees\.gopath'
$env:GOTOOLCHAIN='local'

go test ./...
go vet ./...
go run ./cmd/e2e -out e2e-output.json
```

Ожидаемо: все пакеты `ok`; e2e — `status: done`, 6 вопросов, 4 сущности,
5 узлов, 1 конфликт; 7 вызовов fake-модели.

## Как это подключено к backend

- `POST /api/v1/runs` создаёт run, запускает `chat.Service` и переводит стадию в `interview`;
- `GET/POST /api/v1/runs/{id}/messages` читают историю и принимают ответ респондента;
- `GET /api/v1/runs/{id}/events` отдаёт `agent_thinking`, `agent_message`, `run_status` вместе со стадиями;
- при отсутствии `OPENROUTER_API_KEY` используется `chat.UnavailableClient`, интервьюер задаёт fallback-вопросы.

## Что осталось подключить

1. Хранить сообщения в PostgreSQL, а не только в памяти процесса.
2. Добавить `GET /api/v1/runs` для списка исследований, если он нужен фронту.
3. Согласовать `evidence.ref_id` и `agent_messages.dedup_key` с владельцем БД.
4. Подключить JWT-токен на фронтенде и проверить CORS.
5. Проверить финализацию на живом OpenRouter: аналитик, синтезатор и сохранение дерева.
