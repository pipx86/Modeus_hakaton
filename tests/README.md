# Тесты проекта

Это единая точка входа: здесь видно, какие тесты есть, что они проверяют и
как их запускать.

## Почему unit-тесты лежат рядом с кодом

В Go unit-тест физически должен находиться в том же каталоге, что и пакет:
он проверяет неэкспортированные функции и использует внутренние фикстуры.
Если перенести такой файл в `tests/`, он перестанет компилироваться.
Поэтому здесь собраны:

- карта всех тестов;
- скрипт запуска;
- внешние e2e-тесты, которым не нужен доступ к внутренностям пакетов.

## Быстрый запуск

Из корня репозитория:

```powershell
go test ./...
```

Только этот каталог:

```powershell
go test ./tests/...
```

С покрытием:

```powershell
.\tests\run.ps1 -Coverage
```

## Карта тестов

### Агенты и контракты

- [contract_test.go](../internal/agent/contract_test.go) — контракты входа и выхода, JSON-схемы, отказ при отсутствии evidence.
- [interviewer/agent_test.go](../internal/agent/interviewer/agent_test.go) — вопросы, уточнения, fallback, 5 диалогов.
- [analyst/agent_test.go](../internal/agent/analyst/agent_test.go) — извлечение сущностей, цитаты, дедуп, таксономия.
- [synthesizer/agent_test.go](../internal/agent/synthesizer/agent_test.go) — сборка дерева, конфликты, приоритет источников.

### Промпты

- [prompts/registry_test.go](../internal/prompts/registry_test.go) — версии, checksum, поиск активной версии.
- [prompts/render_test.go](../internal/prompts/render_test.go) — подстановка переменных, shared-правила.

### LLM

- [llm/circuitbreaker_test.go](../internal/llm/circuitbreaker_test.go) — открытие,полуоткрытие и закрытие предохранителя.
- [llm/guarded_test.go](../internal/llm/guarded_test.go) — injection, PII, jailbreak, canary, audit.
- [llm/validate/validate_test.go](../internal/llm/validate/validate_test.go) — проверка JSON по схеме.
- [llm/repair/repair_test.go](../internal/llm/repair/repair_test.go) — repair-цикл после невалидного ответа.
- [llm/openai/client_test.go](../internal/llm/openai/client_test.go) — OpenAI-совместимый клиент, таймауты, ошибки.
- [llm/openai/config_test.go](../internal/llm/openai/config_test.go) — конфигурация из env.
- [llm/ollama/client_test.go](../internal/llm/ollama/client_test.go) — Ollama-клиент.
- [llm/ollama/config_test.go](../internal/llm/ollama/config_test.go) — конфигурация Ollama.
- [llm/agents/config_test.go](../internal/llm/agents/config_test.go) — конфиги трёх агентов.
- [llm/agents/runner_test.go](../internal/llm/agents/runner_test.go) — параллельный шлюз, лимиты, retry.

### Guardrails и чат

- [guardrails/guardrails_test.go](../internal/guardrails/guardrails_test.go) — injection, PII, jailbreak, маскирование.
- [chat/chat_test.go](../internal/chat/chat_test.go) — сессия чата, SSE-события, финализация после интервью.

### API, домен и хранилище

- [api/full_flow_test.go](../internal/api/full_flow_test.go) — сквозной сценарий API.
- [api/handlers_test.go](../internal/api/handlers_test.go) — CRUD и ошибки.
- [api/resume_test.go](../internal/api/resume_test.go) — resume незавершённых прогонов.
- [api/sse_test.go](../internal/api/sse_test.go) — SSE-события.
- [api/tree_integration_test.go](../internal/api/tree_integration_test.go) — дерево и PostgreSQL.
- [domain/contract_test.go](../internal/domain/contract_test.go) — контракт domain-типов.
- [domain/orchestrator_test.go](../internal/domain/orchestrator_test.go) — стадии и переходы.
- [domain/transitions_test.go](../internal/domain/transitions_test.go) — допустимые переходы.
- [domain/tree_validation_test.go](../internal/domain/tree_validation_test.go) — валидация дерева.
- [auth/guard_test.go](../internal/auth/guard_test.go) — проверка ролей.
- [auth/jwt_test.go](../internal/auth/jwt_test.go) — выпуск и проверка JWT.
- [auth/middleware_test.go](../internal/auth/middleware_test.go) — middleware.
- [idempotency/middleware_test.go](../internal/idempotency/middleware_test.go) — повторные запросы.
- [idempotency/store_test.go](../internal/idempotency/store_test.go) — хранилище ключей.
- [search/service_test.go](../internal/search/service_test.go) — поиск.

### Анализ, ingestion, embeddings

- [analysis/gap_test.go](../internal/analysis/gap_test.go) — gap-анализ.
- [analysis/levels_test.go](../internal/analysis/levels_test.go) — уровни.
- [analysis/skills_test.go](../internal/analysis/skills_test.go) — навыки.
- [analysis/tree_test.go](../internal/analysis/tree_test.go) — дерево.
- [ingestion/vacancy_test.go](../internal/ingestion/vacancy_test.go) — вакансии.
- [ingestion/standard_test.go](../internal/ingestion/standard_test.go) — стандарты.
- [ingestion/education_test.go](../internal/ingestion/education_test.go) — образование.
- [ingestion/education_extract_test.go](../internal/ingestion/education_extract_test.go) — извлечение.
- [ingestion/taxonomy_test.go](../internal/ingestion/taxonomy_test.go) — таксономия.
- [embeddings/ollama_test.go](../internal/embeddings/ollama_test.go) — embeddings.

### Сервер и e2e

- [cmd/server/main_test.go](../cmd/server/main_test.go) — конфиг и запуск.
- [cmd/server/finalize_test.go](../cmd/server/finalize_test.go) — транскрипт, UUID и финализация.
- [cmd/server/server_restart_test.go](../cmd/server/server_restart_test.go) — рестарт.
- [cmd/server/startup_schema_test.go](../cmd/server/startup_schema_test.go) — проверка схемы.
- [offline_e2e_test.go](offline_e2e_test.go) — запускает офлайн e2e из этого каталога.

## Интеграционные тесты с PostgreSQL

Часть тестов требует отдельную БД. Задай:

```powershell
$env:INGEST_TEST_DATABASE_URL = 'postgres://...'
$env:DB_TEST_DATABASE_URL = 'postgres://...'
go test -tags integration ./...
```

Без этих переменных тесты с БД пропускаются.

## Что проверять перед PR

```powershell
go test ./...
go vet ./...
.\tests\run.ps1
```
