# AI-слой activity-descriptor

Ветка `feature/ai-on-backend`: AI-часть системы, которая описывает профессиональную деятельность через интервью, анализ источников и синтез дерева.

Ветка собрана поверх актуального `feature/Mikrozaym/BackEnd` и не меняет поведение его API.

## Что внутри

- `interviewer`: задаёт вопросы по уровням роль, функция, задача, действие, уточняет расплывчатые ответы;
- `analyst`: извлекает функции, задачи и действия, требует цитату для каждой сущности, убирает дубли;
- `synthesizer`: собирает дерево из интервью, вакансий и стандартов, разрешает конфликты по приоритету источников;
- системные промпты v1 и файловый реестр версий;
- LLM-клиенты Ollama и OpenRouter/Nemotron с таймаутами, повторами и circuit breaker;
- guardrails: защита от prompt injection, PII и jailbreak;
- offline e2e на fake-модели и команда проверки Nemotron.

## Быстрый старт

```powershell
go test ./...
go vet ./...
go run ./cmd/e2e -out e2e-output.json
```

Для живого Nemotron положи ключ в `.secrets/openrouter.key` и запусти:

```powershell
go run ./cmd/nemotron-smoke -parallel
```

## Проверки

- `go test ./...`: все пакеты проходят;
- `go vet ./...`: без замечаний;
- `go run ./cmd/e2e`: интервью, анализ и синтез завершаются, дерево сохраняется в JSON.

## Интеграция с backend

Сейчас AI-слой подключён как набор пакетов и тестов. Следующий шаг:

1. подключить интервьюера к `internal/steps/interview.go`;
2. добавить `GET/POST /api/v1/runs/{id}/messages`;
3. добавить SSE-события `agent_thinking`, `agent_message` и `run_status`;
4. подключить аналитика и синтезатора после завершения интервью.

Подробности в `docs/AI_LAYER.md`.

## Документация

- `docs/AI_LAYER.md`: состав ветки и порядок подключения;
- `docs/agents/AGENT_IO.md`: входы и выходы агентов;
- `docs/NEMOTRON.md`: настройка OpenRouter и лимиты;
- `docs/E2E.md`: offline e2e;
- `docs/LLM_GUARDRAILS.md`: защита промптов и PII;
- `docs/db/LLM_DATA_MODEL.md`: что хранить в БД по LLM.
