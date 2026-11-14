# План мержа веток

Цель: собрать рабочую интеграцию без потери backend-изменений и без старого
Python-наследия.

## Порядок

1. Взять за базу **`origin/feature/Mikrozaym/BackEnd`** (`7d1580d`), а не `main`.
   В `main` пока пусто, и туда рано мержить.
2. Влить **`origin/feature/rustam/goose-migrations`** (`c500367`) как источник
   схемы БД.
3. На результат влить **`feature/ai-on-backend`** (`455929e`).
4. После AI влить DevOps: **не мержить ветку целиком**, а перенести её коммиты
   `e811ab7`, `06b4fe4` и `f062fdd` через cherry-pick.
5. Frontend подключать отдельно: у ветки нет общего предка с backend, нужен
   общий base или ручной перенос.

## Что мержить с нашей стороны

### AI: мержить

- `internal/agent/**`;
- `internal/guardrails/**`;
- `internal/llm/**` целиком, кроме конфликтующих схем;
- `internal/prompts/**` и `prompts/**`;
- `internal/chat/**`;
- `internal/pipeline/**`;
- `cmd/e2e`, `cmd/nemotron-smoke`, `cmd/jailbreak-smoke`, `cmd/llm-smoke`;
- `cmd/server/finalize.go`;
- `tests/**`;
- docs AI.

### AI: не переносить целиком

- старые `internal/api/handlers.go`, `internal/api/sse.go` — брать версию
  BackEnd и добавлять только chat-часть;
- `internal/domain/types.go` — брать BackEnd/DB и добавлять только интервью,
  конфликты и degraded;
- `README.md` — брать BackEnd, добавлять короткие ссылки на AI и DevOps.

### DevOps: мержить

- `Dockerfile`, `.dockerignore`, `docker-compose.yml`, `.env.example`;
- `Makefile`, `.golangci.yml`;
- `cmd/api/**`, `internal/metrics/**`;
- `web/**`, `deploy/**`, `scripts/check_deploy.py`;
- `.github/workflows/ci.yml`;
- docs DOCKER, METRICS, GRAFANA, N8N, CI.

### DevOps: не мержить

- ветку `feature/devops-go` целиком: она сделана от старой AI-ветки и принесёт
  устаревшие AI-файлы;
- старые `ci/go-pipeline` и `chore/devops-go-infrastructure` — Python-наследие.

## Конфликты, которые будут

### BackEnd + AI (8 файлов)

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
- из domain оставляем `RefID`, `RunType` и новые backend-поля, добавляем только
  `Respondent`, `Question`, `InterviewSession`, `Conflict`, `RunDegraded`;
- в схемах оставляем `ref_id` из БД и добавляем только `priority` и `degraded`,
  если их ещё нет.

### BackEnd + DB (10 файлов)

- `.gitignore`;
- `README.md`;
- `cmd/webhook-worker/main.go`;
- `go.mod`, `go.sum`;
- `internal/outbox/worker.go`;
- `internal/store/db/models.go`;
- `internal/store/store.go`;
- `internal/webhook/sender.go`;
- `notes.md`.

Политика: store/db и миграции берём из DB-ветки, API-обвязку — из BackEnd.
После мержа обязательно `go mod tidy` и `go test ./...`.

### DB + AI

Дополнительно конфликтуют `internal/domain/types.go`, `internal/store/db/*`,
`internal/store/store.go` и схемы LLM. Поэтому AI мержим **после** DB, когда
`ref_id` и `runs.type` уже в базе.

### AI + DevOps

Текстовых конфликтов между последними AI и DevOps нет, но DevOps-ветка старая,
поэтому её коммиты cherry-pick, а не merge.

## Порядок команд завтра

```powershell
git fetch --all
git switch -c integration origin/feature/Mikrozaym/BackEnd

git merge origin/feature/rustam/goose-migrations
# разрешить store/db/go.mod, затем:
go mod tidy
go test ./...

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
- `docker build -t activity-descriptor-api:integration .`;
- `docker compose up -d --build api web postgres`;
- `curl /health`, `/metrics`;
- чат: создать run, получить первый вопрос, ответить;
- финализация: после 6 ответов проверить `root_node_id` и дерево.

## Что не делать

- не мержить `feature/integration` — устарела;
- не мержить `ci/go-pipeline` и `chore/devops-go-infrastructure`;
- не мержить `feature/llm-ollama-client` отдельно: её код уже в AI-ветке;
- не тащить нашу старую миграцию `runs_stage`: она дублирует БД.
