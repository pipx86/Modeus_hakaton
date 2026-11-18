# Конфигурация

## Принципы

- Backend (`cmd/api` — демо, `cmd/server` — production с PostgreSQL) настраивается
  **только переменными окружения**. Отдельного конфигурационного файла нет.
- Шаблон всех переменных — [`.env.example`](../.env.example). Локальный `.env`
  создаётся командой `make env` (копия шаблона) и в Git не попадает.
- `docker-compose.yml` прокидывает переменные в сервисы; значения по умолчанию в
  шаблоне — локальные заглушки, а не production-секреты.
- Секреты (JWT, LLM-ключи, пароли БД, SMTP) хранятся только в локальном `.env`
  или в `.secrets/`. Они перечислены в `.gitignore` (`.env`, `.env.*`,
  `.secrets/`, исключение — `.env.example`) и не коммитятся. В репозитории,
  документации и логах секретов быть не должно.
- `.env.example` содержит только заглушки и комментарии; реальный `.env`
  коммитить нельзя.

## Группы переменных

Полный список с комментариями — в [`.env.example`](../.env.example); ниже ключевые
группы.

| Группа | Переменные | Назначение |
| --- | --- | --- |
| Приложение | `APP_ENV`, `LOG_LEVEL`, `HTTP_ADDR` / `API_PORT`, `WEB_PORT` | Режим, уровень логов, адрес demo API и порт web-контейнера. |
| JWT / auth | `JWT_SECRET` | HMAC-секрет для Bearer-токенов защищённых `/api/v1`. Production задаёт его из секрет-хранилища (не короче 32 байт). Пустой `JWT_SECRET` ⇒ защищённые маршруты отвечают `401`. Токен для локальной проверки: `make token` (`cmd/local-token`) при заданном `JWT_SECRET`. |
| База данных | `DATABASE_URL`; `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB`, `POSTGRES_PORT` | `DATABASE_URL` нужен `cmd/server` (PostgreSQL + pgvector). `cmd/api` работает на in-memory store и БД не требует. `POSTGRES_*` — параметры контейнера `postgres` в compose. |
| LLM | `LLM_BASE_URL`, `LLM_API_KEY`, `LLM_API_KEY_FILE`, `LLM_MODEL`; по агентам — `LLM_INTERVIEWER_*`, `LLM_ANALYST_*` (`MODEL`, `FALLBACK_MODEL`, `TEMPERATURE`, `MAX_TOKENS`, `TIMEOUT_S`), `LLM_TIMEOUT_S` | OpenAI-совместимый провайдер и параметры интервьюера/аналитика. Без ключа интервью работает на детерминированных fallback-вопросах. `FALLBACK_MODEL` включает резервную модель при timeout/unavailable/пустом ответе. Ключ можно держать в файле `.secrets/` через `LLM_API_KEY_FILE`. |
| OpenTelemetry | `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_INSECURE` | OTLP/HTTP-экспорт трейсов (`cmd/server`). Пустой endpoint — спаны не уходят в сеть. |
| n8n | `N8N_PORT`, `N8N_HOST`, `N8N_WEBHOOK_URL`, `N8N_BASIC_AUTH_USER`, `N8N_BASIC_AUTH_PASSWORD`, `N8N_DB_NAME`, `N8N_DB_USER`, `N8N_DB_PASSWORD`, `INGESTION_URL`, `TELEGRAM_CHAT_ID` | Профиль `automation`: порт/URL n8n, basic-auth (сменить пароль), отдельная БД n8n, URL ingestion-эндпоинта и Telegram-чат для алертов. |
| hh.ru | `HH_CLIENT_ID`, `HH_CLIENT_SECRET`, `HH_ACCESS_TOKEN`, `HH_USER_AGENT`, `HH_TOKEN_URL` | OAuth-креды приложения hh.ru. `HH_ACCESS_TOKEN` имеет приоритет; иначе `cmd/ingest` обменивает client id/secret по `client_credentials`. Реальные значения — только в `.env`/`.secrets`, см. [HH_INTEGRATION.md](HH_INTEGRATION.md). |
| SMTP | `SMTP_HOST`, `SMTP_PORT`, `SMTP_USER`, `SMTP_PASS`, `MAIL_FROM`, `MAIL_TO` | Письма из workflow `notion_email_notifications.json`. Пустые значения отключают отправку. |
| Мониторинг | `PROMETHEUS_PORT`, `GRAFANA_PORT`, `GRAFANA_ADMIN_USER`, `GRAFANA_ADMIN_PASSWORD` | Профиль `monitoring`; пароль Grafana — локальная заглушка, менять при любом внешнем доступе. |

Документация по отдельным подсистемам: [DOCKER.md](DOCKER.md), [N8N.md](N8N.md),
[GRAFANA.md](GRAFANA.md), [METRICS.md](METRICS.md), [OBSERVABILITY.md](OBSERVABILITY.md),
[LLM_SETTINGS.md](LLM_SETTINGS.md), [CI.md](CI.md).

## Намеренные артефакты репозитория

Это не мусор — их удалять не нужно:

- `cmd/` — все Go-команды (demo API, production server, миграционные и smoke-утилиты).
- `api-docs/` — статические Swagger UI и OpenAPI-спецификация, которые API отдаёт
  по `/swagger/` (маршрут в whitelist auth middleware; остальные `/api/v1`
  требуют Bearer-токен). Спецификацию обновлять вместе с маршрутами.
- `postman/` — импортируемая Postman-коллекция с примерами запросов. Служебный
  каталог `.postman/` (workspace-метаданные Postman) в репозитории не нужен.
- `.env.example` — шаблон конфигурации без секретов.
- `requirements.txt` — Python-зависимости для скриптов (`PyYAML` для
  `scripts/check_deploy.py`); сам backend — на Go, Python в runtime не нужен.
  `requirements-dev.txt` — опциональные dev-инструменты Python (pytest, ruff),
  подключается поверх runtime-зависимостей.

## Проверки

- `python scripts/check_deploy.py` (`make check-deploy`, CI job `deploy-config`) —
  **только статическая валидация**: YAML/JSON DevOps-ассетов, Dockerfile,
  compose, Grafana/Prometheus, n8n-workflow, обязательных файлов, ссылок в
  документации и сканирование на утечки секретов. Скрипт не деплоит и не
  запускает приложение.
- `python -m pip install -r requirements.txt` нужен лишь для запуска этой проверки
  и других скриптов из `scripts/`, не для сборки Go-бинарей.
