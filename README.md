# Modeus / Activity Descriptor

Сервис анализа профессиональной деятельности: чат-интервью с сотрудником,
извлечение компетенций, проверка цитат-доказательств и построение дерева
компетенций для дальнейшего обучения.

## Архитектура

```text
interviewer  → задаёт вопросы и ведёт диалог
analyst      → извлекает компетенции и evidence из ответов/источников
synthesizer  → детерминированно собирает дерево и конфликты,
               LLM нормализует только человекочитаемые title/description
```

AI-слой работает через OpenAI-совместимый провайдер. Модели задаются
переменными окружения (`LLM_INTERVIEWER_*`, `LLM_ANALYST_*`,
`LLM_SYNTHESIZER_*`). Синтезатор — гибридный агент: id, иерархию, evidence,
победителей конфликтов и приоритеты определяет детерминированный merge в коде,
а LLM вызывается только для нормализации `title`/`description` уже собранных
узлов. Сбой нормализации не роняет прогон: остаётся детерминированное дерево.

## Стек

- Go 1.26.10, `net/http`, pgx v5, sqlc;
- PostgreSQL 16 + pgvector;
- goose (основные и опциональные миграции);
- React 19 + Vite + TypeScript;
- Docker Compose, nginx, Prometheus, Grafana, Jaeger;
- n8n + локальный Postfix relay для уведомлений;
- Ollama + `nomic-embed-text` для семантического поиска.

## Быстрый старт на хосте

### 1. Окружение

```bash
cp .env.example .env
```

Заполни минимум:

```text
DATABASE_URL=postgres://activity:activity_dev_password@127.0.0.1:5432/activity_descriptor?sslmode=disable
JWT_SECRET=<случайная строка не короче 32 байт>
LLM_API_KEY=<ключ провайдера>
LLM_BASE_URL=<OpenAI-совместимый base URL>
LLM_INTERVIEWER_MODEL=deepseek-v4.1-flash-1017
LLM_ANALYST_MODEL=minimax-m3.1-flash
```

Локальные ключи можно держать в `.secrets/`; каталог исключён из git.

### 2. База и миграции

```bash
goose -dir migrations postgres "$DATABASE_URL" up
```

Опциональные векторные миграции:

```bash
goose -table goose_vector_versions \
  -dir migrations/optional/vector postgres "$DATABASE_URL" up
```

### 3. Backend

```bash
go run ./cmd/server
```

Проверка:

```bash
curl http://127.0.0.1:8080/api/v1/health
```

Swagger UI: `http://127.0.0.1:8080/swagger/`. Исходная спецификация —
[openapi.yaml](openapi.yaml), статические файлы — [docs](docs/).

### 4. Frontend

```bash
cd frontend
npm ci
npm run dev
```

Vite proxy отправляет `/api` на `http://127.0.0.1:8080`.

## Docker

Демо-стек:

```bash
docker compose up -d --build
```

Полный backend + frontend на одном origin:

```bash
docker compose --profile server up -d --build
```

Порты по умолчанию:

```text
8080  backend / demo API
8081  demo web
8083  full-stack web (nginx + SPA + API proxy)
5432  PostgreSQL (host-only)
9090  Prometheus (host-only)
3000  Grafana (host-only)
16686 Jaeger UI (host-only)
```

Векторные миграции в `server`-профиле применяются сервисом `migrate-vector`.

## API и аутентификация

- публичные: `GET /api/v1/health`, `GET /api/v1/ready`, `POST /api/v1/auth/register`, `POST /api/v1/auth/login`, `/swagger/`;
- остальные `/api/v1/*` требуют JWT;
- роли: `admin` и `user`;
- для локальной проверки: `go run ./cmd/local-token -role admin`.

Основные сценарии: создание прогона, чат-интервью, batch-прогон, дерево,
evidence, конфликты, ревью, экспорт, сравнение версий, стоимость и
семантический поиск.

## Модели и качество

Текущая конфигурация:

```text
interviewer: deepseek-v4.1-flash-1017
analyst:     minimax-m3.1-flash
synthesizer: deepseek-v4.1-flash-1017
fallback:    deepseek-v4.1-flash-1017
```

Модели выбирались по JSON-контракту, latency, стоимости completion-токенов и
количеству evidence. Merge дерева детерминированный, поэтому id, иерархия,
evidence и конфликты воспроизводимы. LLM синтезатора используется только для
best-effort нормализации `title`/`description`; при ошибке модели, парсинга или
валидации возвращается детерминированный результат.

## Семантика

```text
OLLAMA_URL=http://127.0.0.1:11434
OLLAMA_EMBED_MODEL=nomic-embed-text
```

Индекс источников:

```powershell
.\scripts\Index-VectorSources.ps1
```

Поиск работает в режимах `vector` и `hybrid`; при недоступной Ollama
используется BM25. Таблицы и инструкция: `migrations/optional/vector`.

## Уведомления n8n

После завершения прогона backend пишет событие `run.completed` в outbox.
Отдельный worker отправляет его в n8n:

```powershell
$env:DATABASE_URL = "<database url>"
$env:WEBHOOK_URL  = "https://<host>/webhook/run-completed"
$env:WEBHOOK_SECRET = "<shared secret>"
go run ./cmd/webhook-worker
```

n8n workflow: `deploy/n8n/workflows/run_completed_email.json`.
Доставка — at-least-once, дедупликация по `run_id`.

## Тесты

Локальный CI-эквивалент:

```powershell
powershell -ExecutionPolicy Bypass -File scripts/ci-local.ps1
```

Он запускает:

```text
go build ./...
go vet ./...
go test ./...
python scripts/check_deploy.py
docker compose config
frontend lint / tests / build
```

Отдельно:

```bash
go test ./...
cd frontend && npm test -- --run
```

## Секреты

- не коммить `.env`, `.secrets/`, SMTP/LLM-ключи;
- `prompts/_shared/` добавлен в `.gitignore`;
- секреты n8n хранятся в credential store n8n, а не в workflow JSON;
- webhook подписывается HMAC-SHA256.

## Ограничения

- для production нужны собственный TLS-домен, Alertmanager, HA и retention;
- Gmail/local Postfix relay — демонстрационный транспорт, production требует
  стабильного SMTP-провайдера;
- Cloudflare quick tunnel временный;
- публичный webhook требует отдельной аутентификации;
- Ollama нужен только для семантического поиска; без него остаётся BM25.

## Зависимости

Список сторонних зависимостей: [DEPENDENCIES.md](DEPENDENCIES.md).
