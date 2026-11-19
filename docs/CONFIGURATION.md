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
| Семантический поиск | `OLLAMA_URL`, `OLLAMA_EMBED_MODEL`, `RAG_SEARCH_STRATEGY`, `RAG_SEARCH_LIMIT`, `SOURCE_AUTO_INDEX` | Эмбеддинги Ollama для `source_fragments`/`fragment_embeddings` и RAG-ретривера. Умолчания: `http://127.0.0.1:11434`, `nomic-embed-text`, `vector`, `8`, `true`. Нужны pgvector и `migrations_optional/vector`; существующие источники догоняет `go run ./cmd/index-sources`. Подробности — [EMBEDDINGS.md](EMBEDDINGS.md). |
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

## Резервная копия PostgreSQL на Windows

Задайте `PGHOST`, `PGPORT`, `PGUSER`, `PGDATABASE` для конкретной базы.
Пароль передайте через `PGPASSFILE` с ограниченным доступом или через защищённое
окружение процесса. Создайте целевую папку на диске D вне Git и выполните:

```powershell
./scripts/Backup-Database.ps1 -PgBin 'D:\PostgreSQL\16\bin' -Output 'D:\ModeusBackups\modeus.dump'
```

Скрипт оставляет готовый файл только после проверки оглавления архива.
Восстановление проверяйте через `pg_restore --exit-on-error` в отдельную пустую
базу. Дамп содержит исходные данные, поэтому доступ к файлу следует ограничить.

## Помесячный отчёт о вакансиях

Для JSON/CSV можно передать необязательное поле `published_at` с датой,
временем и часовым поясом. Пример: `2026-11-15T09:00:00+03:00`. Поиск hh.ru
использует дату из ответа, если она есть. Повторная выгрузка без даты сохраняет
известную дату публикации. После миграций сформируйте отчёт по части заголовка:

```powershell
go run ./cmd/ingest vacancy-trends -query 'Backend' -from 2026-09-01 -until 2026-12-01
```

Группы отчёта показывают число сохранённых публикаций по месяцам UTC и
поставщикам; записи без даты считаются отдельно. Период полуоткрытый и не
может быть дольше 24 месяцев. Представительность выборки зависит от источников
и частоты импорта.

## История навыков и совместный импорт вакансий

Команда совместного импорта принимает один или оба файла выгрузки hh.ru.
Подробный файл дополняет основные записи с тем же идентификатором.
Счётчики разделяют вставки, изменения и пропуски.

```bash
go run ./cmd/ingest export-vacancies -basic basic.json -detailed detailed.json
go run ./cmd/ingest skill-history -dir /path/to/history
go run ./cmd/ingest forecast-audit -series hh_vacancies -top 20
```

История принимает `freq_*.json` с частотами и датой в `_meta.snapshot_date`
или в имени файла. Рыночная серия называется `hh_vacancies`, серия профессии
использует `_meta.profession`. Файл с пометкой `synthetic` отклоняется.
Повтор одной и той же месячной серии без изменений не создаёт новых записей.

`GET /api/v1/forecast/observed` измеряет изменение по непрерывному месячному
окну. `GET /api/v1/forecast/engine` показывает доступные месяцы, а
`/forecast/{skill}`, `/forecast/all`, `/forecast/top` и `/forecast/popular`
возвращают линейный прогноз. Поле `forecast_months` показывает фактический
горизонт, ограниченный числом наблюдений. `forecast-audit` проверяет точность
на последней точке, исключённой из обучения, и показывает пропуски месяцев.
Для оценки навыка нужно минимум четыре его наблюдения.

## Подписки на публикации

Пользовательские маршруты `/api/v1/subscriptions` и `/api/v1/notifications`
используют владельца из JWT. Внешний сборщик с операторским JWT может получить
темы из `GET /api/v1/internal/subscriptions` и отправить проверенные результаты
в `POST /api/v1/webhooks/new-publications`. Сборщик продолжает пагинацию с
`after_id`, равным идентификатору последней подписки из предыдущей страницы.
Тело callback содержит `subscription_id` и массив `articles` с полями
`title`, `abstract`, `url` и `source` (`openalex` или `arxiv`). Повтор
с тем же URL для подписки не создаёт дубль. Уведомления доступны внутри API.

## Файловый импорт по расписанию

Для `POST /api/v1/ingest` запустите `cmd/server` с PostgreSQL. Задайте
`INGEST_VACANCIES_FILE` (JSON или CSV) и при необходимости
`INGEST_VACANCIES_FORMAT` (по умолчанию JSON). Для профстандартов задайте
`INGEST_STANDARD_FILE` и стабильный `INGEST_STANDARD_KEY`.
`INGEST_STANDARD_FORMAT` по умолчанию JSON. Для PDF укажите формат и
`INGEST_STANDARD_TITLE`. Пути должны быть доступны процессу сервера.

В n8n задайте `INGESTION_URL` с этим маршрутом и добавьте
`Authorization: Bearer …` через учётные данные HTTP Request. Нужен JWT роли
admin. Тело принимает `sources: ["vacancies", "standards"]` или один из этих
источников. Повторный запрос не создаёт дубликаты. Если сбой возник уже после
загрузки одного файла, повтор использует тот же постоянный ключ и продолжает
проверку остальных источников.

Для повторов передавайте стабильный Idempotency-Key или X-Run-ID.
Сервер хранит результат запроса для владельца JWT в PostgreSQL один час.
