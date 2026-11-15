# План мержа веток

Зафиксировано: 15.11.2026, 19:50.

Цель: собрать рабочую интеграцию без потери backend-изменений, без старого
Python-наследия и без лишних конфликтов.

## Зафиксированные ветки

| Ветка | Коммит | Что это |
|---|---|---|
| `origin/feature/Mikrozaym/BackEnd` | `7d1580d` | база интеграции |
| `feature/db-llm` | `f58c82c` | схема БД: LLM + runtime |
| `origin/feature/ai-on-backend` | `8c8b8e2` | AI, чат, агенты, метрики |
| `origin/feature/devops-go` | `f062fdd` | Docker, CI, Grafana, n8n |
| `origin/feature/frontend` | `f37e2df` | фронт, подключается отдельно |

## Порядок

1. Взять за базу **`origin/feature/Mikrozaym/BackEnd`** (`7d1580d`).
   `main` пока пустой, туда рано.
2. Из **`feature/db-llm`** (`f58c82c`) перенести **только `migrations/`**.
   Код БД-ветки не мержить: он конфликтует с BackEnd в store, outbox, webhook
   и `go.mod`.
3. На результат перенести **AI-ветку** (`8c8b8e2`):
   - новые каталоги целиком;
   - 8 общих файлов правим вручную.
4. После AI перенести DevOps: **не мержить ветку целиком**, а cherry-pick
   коммиты `e811ab7`, `06b4fe4` и `f062fdd`.
5. Frontend подключать отдельно: у ветки нет общего предка с backend, нужен
   общий base или ручной перенос.

## Почему БД только миграциями

BackEnd уже умеет работать с `ref_id`, `runs.type` и `run_sse_events`.
Для этого нужна схема, а не код `internal/store` из DB-ветки.

Если мержить DB-ветку кодом, получаем 10 конфликтов:

- `.gitignore`;
- `README.md`;
- `cmd/webhook-worker/main.go`;
- `go.mod`, `go.sum`;
- `internal/outbox/worker.go`;
- `internal/store/db/models.go`;
- `internal/store/store.go`;
- `internal/webhook/sender.go`;
- `notes.md`.

Поэтому в интеграцию идут только `migrations/`.

## Что переносить из AI

### Переносить целиком

- `internal/agent/**`;
- `internal/guardrails/**`;
- `internal/llm/**`, кроме конфликтующих схем;
- `internal/prompts/**` и `prompts/**`;
- `internal/chat/**`;
- `internal/pipeline/**`;
- `internal/steps/ai_steps.go`;
- `cmd/e2e`, `cmd/nemotron-smoke`, `cmd/jailbreak-smoke`,
  `cmd/temperature-smoke`, `cmd/llm-smoke`;
- `cmd/server/finalize.go`;
- `tests/**`;
- docs AI.

### Не переносить целиком

- старые `internal/api/handlers.go`, `internal/api/sse.go` — брать версию
  BackEnd и добавлять только chat-часть;
- `internal/domain/types.go` — брать BackEnd и добавлять только интервью,
  конфликты и degraded;
- `README.md` — брать BackEnd, добавлять короткие ссылки на AI и DevOps.

## Конфликты BackEnd + AI (8 файлов)

- `README.md`;
- `cmd/server/main.go`;
- `internal/api/handlers.go`;
- `internal/api/sse.go`;
- `internal/domain/types.go`;
- `internal/llm/schema/evidence.schema.json`;
- `internal/llm/schema/node.schema.json`;
- `internal/llm/schema/source.schema.json`.

Политика:

- backend-версию берём за базу;
- chat-маршруты и chat-события добавляем сверху;
- из domain оставляем backend-поля и добавляем только `Respondent`,
  `Question`, `InterviewSession`, `Conflict`, `RunDegraded`;
- в схемах оставляем `ref_id` из BackEnd и добавляем только `priority` и
  `degraded`, если их ещё нет.

## DevOps: что переносить

- `Dockerfile`, `.dockerignore`, `docker-compose.yml`, `.env.example`;
- `Makefile`, `.golangci.yml`;
- `cmd/api/**`, `internal/metrics/**`;
- `web/**`, `deploy/**`, `scripts/check_deploy.py`;
- `.github/workflows/ci.yml`;
- docs DOCKER, METRICS, GRAFANA, N8N, CI.

Не мержить ветку `feature/devops-go` целиком: она сделана от старой AI-ветки.

## Команды мержа

```powershell
git fetch --all
git switch -c integration origin/feature/Mikrozaym/BackEnd

# только миграции, без кода БД-ветки
git checkout feature/db-llm -- migrations/
go mod tidy
go test ./...

# AI: сначала новые каталоги, затем 8 патчей
git merge origin/feature/ai-on-backend
# разрешить 8 файлов, затем:
go test ./...

git cherry-pick e811ab7 06b4fe4 f062fdd
# поправить README/.gitignore, затем:
go test ./...
docker compose config --quiet
```

## Проверка после мержа

- `go test ./...`;
- `go vet ./...`;
- `go run ./cmd/e2e -out e2e-output.json`;
- применить миграции из `migrations/` на чистую БД;
- `docker build -t activity-descriptor-api:integration .`;
- `docker compose up -d --build api web postgres`;
- `curl /health`, `/metrics`;
- чат: создать run, получить первый вопрос, ответить;
- финализация: после 6 ответов проверить `root_node_id` и дерево;
- проверить, что `evidence.ref_id` сохраняется.

## Что не делать

- не мержить `feature/integration` — устарела;
- не мержить `ci/go-pipeline` и `chore/devops-go-infrastructure`;
- не мержить `feature/llm-ollama-client` отдельно;
- не тащить старую миграцию `runs_stage`;
- не мержить код БД-ветки, только миграции.
