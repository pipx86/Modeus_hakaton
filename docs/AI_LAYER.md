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
| `internal/pipeline/` | offline-конвейер интервью → анализ → синтез |
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

## Как подключить к backend (следующий шаг)

1. `internal/steps/interview.go` сейчас заглушка. В `ExecuteWithData` создать
   `interviewer.Agent` + `interviewer.Session` и сохранять вопросы/ответы.
2. Добавить `GET/POST /api/v1/runs/{id}/messages` и SSE-события
   `agent_thinking`, `agent_message`, `run_status` — этого ждёт фронтенд
   (`feature/frontend`, `ChatTab`).
3. После завершения интервью вызвать `analyst.Agent.Analyze` и
   `synthesizer.Agent.Synthesize`; дерево сохранить через существующий store.
4. Подключить `GuardedClient` и `agents.Pool` с `OPENROUTER_*` из env.
5. `evidence.ref_id` и `agent_messages.dedup_key` — отдельные additive-миграции,
   согласовать с владельцем БД.
