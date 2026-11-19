# Security review MVP (activity-descriptor)

Обзор безопасности MVP: аутентификация, webhook/outbox, секреты, данные/PII,
prompt injection, LLM-провайдер и остаточные риски. Основан на чтении кода и
тестах (ветка `feature/roman/ai`, ноябрь 2026); это не пентест.

## 1. Аутентификация и авторизация (JWT)

Код: [`internal/auth/jwt.go`](../internal/auth/jwt.go),
[`internal/auth/middleware.go`](../internal/auth/middleware.go),
[`internal/auth/guard.go`](../internal/auth/guard.go).

- **HS256** (`golang-jwt/jwt/v5`), claims `sub`/`role`/`iat`/`exp`; пароли хранятся как bcrypt-хэши в `users`, серверных сессий нет. JWT выдаётся на 24 часа после `POST /auth/login` или `POST /auth/register`. Отзыва/refresh, `iss`/`aud` нет — токен валиден до `exp`.
- Секрет — только env `JWT_SECRET`; `cmd/server` не стартует с пустым секретом. Проверки **минимальной длины в коде нет**: требование «≥ 32 байт вне локальной разработки» зафиксировано в [docs/DOCKER.md](DOCKER.md), compose-fallback `local-dev-jwt-secret-change-me-32-bytes-min` — только для локального стека.
- Middleware принимает `Authorization: Bearer <jwt>` и (для `EventSource`) `?access_token=`; query-токен не пишется в access-логи (`internal/logging` логирует только path), но может попасть в историю браузера/логи прокси.
- Публичны `GET /api/v1/health`, `POST /api/v1/auth/register`, `POST /api/v1/auth/login`, `/swagger` и `/metrics` (лейблы без run_id/PII). Остальные `/api/v1/*` требуют JWT; роли — `admin` и `user`, проверка явным `RequireRole(...)` в каждом хендлере.

| Роль | Чтение | Запись |
|---|---|---|
| admin | всё | всё: пользователи, источники, дерево, approve/reject, ЗУН, удаление |
| user | прогоны, дерево, ответы, сообщения, снимки, поиск, источники, конфликты, экспорт | создание прогона, интервью, batch-транскрипт |

**Обычный пользователь** может создавать исследования и проходить интервью; экспертные изменения доступны только admin. Риски: в роутере нет default-deny (middleware проверяет только подпись, роль — хендлер: новый хендлер без `RequireRole` откроется всем ролям); **RBAC на уровне прогона отсутствует** — любой аутентифицированный пользователь читает любой run по UUID, `runs.created_by` в HTTP-пути не заполняется.

## 2. Webhook и outbox (SSRF)

Код: [`internal/webhook/sender.go`](../internal/webhook/sender.go),
[`internal/outbox/worker.go`](../internal/outbox/worker.go),
[`cmd/webhook-worker/main.go`](../cmd/webhook-worker/main.go), контракт —
[`internal/webhook/README.md`](../internal/webhook/README.md).

- URL — только из env `WEBHOOK_URL`; `NewSender` требует схему `http`/`https`, непустой host, запрещает userinfo и пустой `WEBHOOK_SECRET`. Таймаут 10 с, редиректы запрещены (`CheckRedirect` → `ErrUseLastResponse`).
- Тело события проверяется как JSON и подписывается **HMAC-SHA256** точных байтов; `X-Webhook-Signature` — hex без префикса; не-2xx — ошибка доставки.
- Триггер БД при `runs.status='done'` создаёт `agent_messages` c `run.completed` (payload: `run_id`, `profession`, `finished_at`; без цитат/PII). Worker: `SELECT ... FOR UPDATE SKIP LOCKED`, доставка **at-least-once**, повтор при ошибке; получатель дедуплицирует по `event`+`run_id`.
- **SSRF-защиты от внутренних адресов нет** (allowlist/private-IP deny/DNS pinning): безопасность опирается на то, что `WEBHOOK_URL` — операторская настройка, а не пользовательский ввод. Приёмник n8n не настроен/не проверен; worker — отдельный процесс, в compose его нет.

## 3. Секреты

- Ключ LLM — только env `LLM_API_KEY` (или файл `LLM_API_KEY_FILE`, по умолчанию `.secrets/reformboss.key`); не логируется, в ошибках конфигурации не печатается, файл ≤ 4096 символов без переводов строк (`internal/llm/agents/config.go`).
- `.secrets/` и `.env*` в [.gitignore](../.gitignore); коммитится только [.env.example](../.env.example) с плейсхолдерами. Живых ключей/токенов в репозитории не найдено: grep по `sk-*`, JWT-префиксам и `BEGIN PRIVATE KEY` даёт только placeholder в `frontend/src/pages/login.tsx` и безобидные id вида `tax-task-dashboards`.
- Экспорт (`GET /runs/{id}/export`) отдаёт только узлы и evidence (source_id/ref_id/quote) — ключей/конфигурации нет. Workflow-файлы n8n без credentials, только placeholder-id (`REPLACE_WITH_SMTP_CREDENTIAL_ID`), креды — в n8n (`deploy/n8n/workflows/README.md`).
- **SMTP-креды управляются n8n** (SMTP credential); env `SMTP_*`/`MAIL_*` — только валидация конфигурации. Notion/SMTP-уведомления — не live-интеграция. Webhook-секрет — HMAC-ключ подписи, не шифрование: получатель обязан проверять подпись и не логировать тела.

## 4. Данные и PII

Сохраняется: `runs`, `nodes`, `evidence` (дословные цитаты), `snapshots.tree`, `sources` (finalizer пишет **полный транскрипт** интервью в `sources.content`), LLM-аудит `llm_requests.messages`/`llm_responses.raw_text`/`llm_usage`/`prompt_versions` (`migrations/20261115000000_llm_observability.sql`, `internal/llmobs`). Чат-сообщения — только в памяти.

- `internal/guardrails` заменяет PII (email/телефон/карта) на `[EMAIL]`/`[PHONE]`/`[CARD]` до провайдера и в ответе; guard-аудит (in-memory ring) хранит только тип/смещение/хэш, таблица `pii_findings` в миграции есть, но код её пока не заполняет. Промпты запрещают персональные данные в ответе (`prompts/_shared/security.md`).
- **LLM-аудит пишется до guard-редакции**: `llmobs.Recorder` оборачивает пул снаружи, поэтому `llm_requests.messages` может содержать исходный текст (транскрипт/ответы) без маскирования; `llm_responses.raw_text` — уже ответ после guard (PII вычищена). `prompt_versions`/`prompt_version_id` приложение пока не заполняет, `pii_findings` — тоже.
- Access-логи без query string/тел/токенов; `/metrics` без run_id/PII; webhook-событие без транскрипта.
- **Retention кодом не реализован**: сроки 90/30/400 дней — только в [docs/db/LLM_DATA_MODEL.md](db/LLM_DATA_MODEL.md). `DELETE /runs/{id}` не чистит LLM-аудит (`llm_requests.run_id` — `ON DELETE SET NULL`) и interview-источник (`sources.run_id` — `SET NULL`): транскрипт остаётся осиротевшей строкой. При сбое `SaveTree` источник уже создан — возможен orphaned source.
- Экспорт без секретов, но с цитатами из интервью — считайте его чувствительным.

## 5. Prompt injection и защита LLM

- Границы доверия и детекторы — [docs/LLM_GUARDRAILS.md](LLM_GUARDRAILS.md): system доверенный, user/assistant/транскрипт/источники — данные.
- Пул `internal/llm/agents` оборачивает клиента в
  [`llm.GuardedClient`](../internal/llm/guarded.go): недоверенные сообщения — в
  маркерах `<<UNTRUSTED_DATA>>`/`<</UNTRUSTED_DATA>>`; injection/jailbreak-паттерны (включая русские, base64/percent-encoded) заменяются на `[BLOCKED_INSTRUCTION]`, PII редактируется, ответ с injection-паттернами блокируется. Политика по умолчанию — `wrap`.
- Утечка системного промпта: сравнение ответа с длинными строками промпта + опциональная canary; при утечке — 1 повтор, затем `ErrSystemPromptLeak`. Canary в проде **не включена** (опция есть в `cmd/jailbreak-smoke`). `*llm.GuardError` не содержит текста промпта/секретов.
- Контракт `agent.io/v1`: ответы interviewer/analyst валидируются JSON-схемами (`internal/agent/schema/`), аналитик проверяет дословность каждой `quote` в transcript; невалидный ответ — repair (по умолчанию 1 повторная попытка, до 2 запросов), затем run → `failed`. Синтезатор детерминированный, модель не вызывает.
- Ограничение: детекторы rule-based; обфускация сложнее base64/percent-encoding, «мягкие»/многошаговые jailbreak не гарантированы. Guardrails — барьер, а не гарантия.

## 6. LLM-провайдер

Код: `internal/llm/agents/config.go` (продакшн), `internal/llm/openai/config.go` (legacy-клиент smoke-команд).

- Generic-режим (`LLM_BASE_URL`/`LLM_MODEL`/`LLM_API_KEY`) требует `LLM_API_KEY`; при base/model без ключа — ошибка «refusing to reuse OPENROUTER_API_KEY for another provider». Легаси-ключ `OPENROUTER_API_KEY` **не подставляется** к кастомному base URL — ключ не уходит чужому провайдеру.
- Legacy-режим OpenRouter включается, только если не задана ни одна generic-переменная; ключ из файла (`LLM_API_KEY_FILE`, fallback `.secrets/reformboss.key`) поддержан для обратной совместимости.
- Ключ не печатается в логах/ошибках; провайдерские ошибки классифицируются без тела ответа и ключей ([docs/LLM_ERRORS.md](LLM_ERRORS.md)).

## 7. Модель угроз (кратко)

| Угроза | Контроль | Остаточный риск |
|---|---|---|
| Подделка/кража JWT | HS256, `exp`, подпись | нет отзыва/refresh; query-token и localStorage на фронте; нет TLS в MVP |
| Повышение прав по роли | `RequireRole` + auth-тесты | нет default-deny; активация промптов admin-only |
| Чтение чужих прогонов | JWT-аутентификация | нет RBAC на run, `created_by` не заполняется |
| SSRF через webhook | URL из env, схема/host/no-userinfo, no-redirect, HMAC | нет allowlist/private-IP deny |
| Утечка ключа LLM | env/файл, gitignore, no-log, pairing rule | доступ к хосту/`.secrets` = доступ к ключу |
| PII в БД/аудите | PII-редакция при вызове модели, ограниченные логи | транскрипт в `sources`, `llm_requests.messages` до редакции и `raw_text` в открытом виде; нет retention; delete run не чистит |
| Prompt injection | untrusted-маркеры, детекторы, output guard, JSON-схемы, evidence-quote | новых формулировок не гарантирует; canary не включена |
| Утечка системного промпта | leak-guard + retry, typed errors | промпты встроены в бинарь (`prompts/README.md`) |
| Отравление вывода | дословные цитаты, приоритет источников, `conflicts` | качество evidence зависит от модели |
| Повтор webhook | уникальный `run.completed`, `SKIP LOCKED`, at-least-once | получатель обязан дедуплицировать; n8n не настроен |
| DoS/расход токенов | LLM-гейт: concurrency 3, 20 rpm, retry, circuit breaker | нет HTTP rate limiting на create-run/messages |
| Публичные `/metrics`, `/health` | лейблы без PII/run_id | `/metrics` нельзя выставлять наружу без auth-proxy |
| Взлом n8n/SMTP/Notion | креды только в env/n8n, placeholder-id | публичный webhook n8n и SMTP требуют TLS/auth/ротации |

## 8. Остаточные риски и следующие шаги

1. **Rate limiting**: ограничить создание прогонов и сообщений по пользователю/IP (сейчас ограничен только пул LLM: `LLM_MAX_CONCURRENCY`/`LLM_REQUESTS_PER_MINUTE`).
2. **RBAC на уровне прогона**: заполнять `runs.created_by` из `sub` JWT и проверять владельца/команду на run-маршрутах; закрыть чтение чужих прогонов студентами и гостями.
3. **Retention/удаление PII**: джоб очистки `llm_responses.raw_text`/`llm_requests`/транскриптов; `DELETE /runs/{id}` должен чистить связанные `sources`, `llm_requests`, `pii_findings`, а не оставлять `SET NULL`.
4. **Публичный URL n8n и SMTP/Notion**: TLS, basic/header auth, `N8N_SECURE_COOKIE=true`, креды только в n8n; после настройки приёмника — e2e-проверка подписи и дедупликации `run.completed`.
5. **Усилить auth**: падать на старте при `JWT_SECRET` короче 32 байт; запретить query-токен для не-SSE маршрутов; отзыв/ротация токенов; уйти от localStorage к памяти/HttpOnly-cookie с CSRF-защитой.
6. **Аудит и CI**: включить canary для leak-guard; security-логи (401/403, активации промптов, удаления) с retention; `govulncheck`/`gosec` в CI (сейчас `.golangci.yml` — только `govet`/`ineffassign`/`misspell`).
7. **Служебные эндпоинты**: закрыть `/metrics` от внешней сети или вынести за auth-proxy; health оставить публичным.
8. **Prompt activation**: активация версий промптов уже admin-only, добавить обязательный reason и аудит.
