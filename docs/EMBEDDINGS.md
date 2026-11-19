# Семантический поиск (эмбеддинги Ollama)

Документ описывает рабочий контур семантического поиска: как источники
превращаются во фрагменты и векторы, как это включить, как догнать уже
существующие материалы и что видно в логах. Код: `internal/search`,
`internal/embeddings`, `internal/store/retrieval.go`, `internal/store/hybrid.go`,
`cmd/index-sources`, `cmd/server/finalize.go`.

## Что работает

1. **Индексация источника** — текст источника режется на фрагменты до 800
   символов с перекрытием 100, каждый фрагмент векторизуется моделью Ollama и
   сохраняется в `source_fragments` + `fragment_embeddings` (vector(768), имя
   модели хранится рядом с вектором).
2. **Автоматическая индексация** — `POST /api/v1/sources`,
   `POST /api/v1/sources/upload` и `POST /api/v1/runs/{id}/sources` запускают
   индексацию в фоне (`SOURCE_AUTO_INDEX`, по умолчанию `true`). Ответ API не
   ждёт эмбеддинги.
3. **Ручная/повторная индексация** — `POST /api/v1/sources/{id}/index`
   (admin, синхронно) или backfill-команда `cmd/index-sources`.
4. **Поиск** — `GET /api/v1/runs/{id}/search?q=...&strategy=vector|hybrid`.
   В ответе есть `mode`: `vector`, `hybrid` или `bm25` (аварийный лексический
   режим). Гибрид объединяет ранги BM25 и векторов методом RRF.
5. **RAG в finalizer** — `cmd/server: newRAGRetriever` подключает тот же сервис:
   `RAG_SEARCH_STRATEGY=vector|hybrid`, `RAG_SEARCH_LIMIT` (top-K фрагментов,
   группируются по источникам). Если Ollama недоступна, поиск деградирует до
   BM25, статус прогона не меняется.

## Требования

- PostgreSQL с расширением **pgvector** и применёнными основными миграциями.
- Необязательный пакет миграций `migrations_optional/vector` (отдельная таблица
  версий Goose). Docker-профиль `server` применяет его автоматически
  (`migrate-vector`), для host-БД — вручную:

  ```powershell
  goose -dir migrations postgres $env:DATABASE_URL up
  goose -table goose_vector_versions -dir migrations_optional/vector postgres $env:DATABASE_URL up
  goose -table goose_vector_versions -dir migrations_optional/vector postgres $env:DATABASE_URL status
  ```

  Проверка схемы:

  ```powershell
  psql $env:DATABASE_URL -c "\dt source_fragments fragment_embeddings"
  psql $env:DATABASE_URL -c "SELECT to_regprocedure('find_relevant_fragments(uuid,vector,text,integer)'), to_regprocedure('find_fragments_bm25(uuid,text,integer)');"
  ```

- **Ollama** с моделью эмбеддингов (по умолчанию `nomic-embed-text`, 768
  измерений):

  ```powershell
  ollama pull nomic-embed-text
  curl.exe -s http://127.0.0.1:11434/api/tags
  ```

## Переменные окружения

| Переменная | По умолчанию | Назначение |
| --- | --- | --- |
| `OLLAMA_URL` | `http://127.0.0.1:11434` | Адрес Ollama для `/api/embed`. Используется API, RAG и backfill. |
| `OLLAMA_EMBED_MODEL` | `nomic-embed-text` | Модель эмбеддингов. Векторы в БД сравниваются только в рамках одной модели. |
| `RAG_SEARCH_STRATEGY` | `vector` | `vector` — только косинусный поиск; `hybrid` — RRF(BM25, vector). Неизвестное значение → `vector` + warning. |
| `RAG_SEARCH_LIMIT` | `8` | Top-K фрагментов для RAG finalizer, допустимо 1–50; неверное значение → 8 + warning. |
| `SOURCE_AUTO_INDEX` | `true` | Фоновая индексация новых источников. `false` отключает вызовы Ollama из обработчиков. |
| `DATABASE_URL` | — | Обязателен для `cmd/server` и `cmd/index-sources`. |

Диапазон значений по умолчанию совпадает с локальной установкой Ollama, поэтому
backend на машине разработчика работает без правки `.env`. Смена
`OLLAMA_EMBED_MODEL` требует переиндексации: старые векторы другой модели не
используются (`find_relevant_fragments` фильтрует по `model`).

## Backfill существующих источников

После применения векторных миграций или смены модели нужно проиндексировать
источники, созданные раньше (новая модель → все источники выглядят
«непроиндексированными»).

```powershell
# План без вызова модели
go run ./cmd/index-sources -dry-run

# Пробный прогон
go run ./cmd/index-sources -limit 10

# Все источники без векторов текущей модели (по умолчанию)
go run ./cmd/index-sources

# Принудительно переиндексировать всё
go run ./cmd/index-sources -all

# Один источник
go run ./cmd/index-sources -source <source-uuid>

# Не возвращать ненулевой код при частичных ошибках (для скриптов)
go run ./cmd/index-sources -allow-failures
```

Флаги: `-limit` (0 — без ограничения), `-source`, `-all`, `-dry-run`,
`-timeout` (на источник, по умолчанию `5m`), `-allow-failures`,
`-ollama-url`, `-embed-model`. Скрипт проверяет векторные таблицы и доступность
Ollama до начала работы, индексирует последовательно (щадит CPU), печатает
прогресс и итог `indexed/failed/total`; код возврата 1 при ошибках. Модель чата
не вызывается — только `/api/embed`.

На хосте с `.env`/`.secrets/llm.env` те же действия выполняет
`scripts/Index-VectorSources.ps1` (не печатает секреты):

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\Index-VectorSources.ps1 -Limit 10
powershell -ExecutionPolicy Bypass -File .\scripts\Index-VectorSources.ps1
```

## HTTP-контракт

`POST /api/v1/sources/{id}/index` (Bearer, admin):

| Статус | Код | Когда |
| --- | --- | --- |
| 204 | — | Индекс перестроен |
| 400 | `BAD_REQUEST` | `id` не UUID |
| 401/403 | `UNAUTHORIZED`/`FORBIDDEN` | Нет токена / не admin |
| 404 | `SOURCE_NOT_FOUND` | Источник не найден |
| 409 | `SOURCE_CHANGED` | Текст изменился во время векторизации — повторите |
| 422 | `EMPTY_SOURCE` | У источника пустой текст |
| 501 | `NOT_IMPLEMENTED` | Сервер без PostgreSQL (demo API) |
| 502 | `INDEX_FAILED` | Ollama/модель недоступны или нет векторных миграций; прежний индекс сохранён |

`GET /api/v1/runs/{id}/search` ищет только среди источников, привязанных к
исследованию (`run_sources`), и возвращает `{"mode": "...", "hits": [...]}`.
Семантический запрос должен быть осмысленным текстом, а не набором ключевых
слов: модель кодирует смысл, а BM25 — совпадение словоформ.

## Ограничения и поведение при сбоях

- Фрагменты — фиксированные окна по 800 символов с перекрытием 100; структура
  документа не учитывается (заголовки/таблицы могут попасть в один фрагмент).
- Поиск возвращает top-K без порога релевантности: при пустом/нерелевантном
  корпусе всё равно вернутся ближайшие фрагменты. Релевантность цитаты
  подтверждается экспертной проверкой.
- `find_relevant_fragments` использует HNSW-индекс; при фильтре по одному
  `run_id` приближённый индекс может вернуть меньше строк, чем ожидается на
  большом корпусе. Качество проверяйте на реальных данных.
- Индексация одного источника ограничена 2 минутами в API (`SOURCE_AUTO_INDEX`)
  и `-timeout` (по умолчанию 5 минут) в backfill. Холодная загрузка модели
  может занять больше времени, чем тёплые запросы.
- Переиндексация удаляет прежние фрагменты источника и связанные векторы
  **всех** моделей, затем пишет векторы текущей модели. Это ожидаемо: смешивать
  модели нельзя.
- Одновременная правка источника во время индексации не портит индекс:
  транзакция проверяет текст перед записью и возвращает `SOURCE_CHANGED`.
- Backfill последовательный; на 131 источнике это минуты, не секунды. Для
  больших корпусов запускайте его вне окна активной нагрузки.

## Логи и наблюдаемость

| Событие | Уровень | Смысл |
| --- | --- | --- |
| `source_auto_index_enabled` | INFO | `cmd/server` стартовал с фоновой индексацией (URL и модель) |
| `source_auto_index_disabled` / `source_auto_index_unavailable` | INFO/WARN | Автоиндексация выключена настройкой или нет PostgreSQL |
| `source_auto_indexed` | INFO | Новый источник проиндексирован (`source_id`, `duration_ms`) |
| `source_auto_index_failed` | WARN | Фоновая индексация не удалась; источник остаётся без векторов до повтора |
| `source_indexed` / `source_index_failed` | INFO/WARN | Синхронный admin-эндпоинт |
| `embedding_request_failed` | WARN | Ollama не ответила на `/api/embed` (адрес, причина) |
| `search_vector_fallback` | WARN | Ollama настроена, но поиск ушёл в BM25 — качество деградировало |
| `search_completed` | DEBUG | Режим, run_id, число hits |
| `rag_retriever_configured` | INFO | Параметры RAG при старте: URL, модель, стратегия, лимит |
| `rag_retrieved` | INFO | Фактический режим RAG (`vector`/`hybrid`/`bm25`), hits, источники, latency |
| `rag_search_strategy_unknown` / `rag_search_limit_invalid` | WARN | Некорректный env, используется безопасное умолчание |

Если в логах есть `search_vector_fallback` или `source_auto_index_failed`,
проверьте `OLLAMA_URL`, что модель скачана (`ollama list`) и применение
векторных миграций — это не «нормальный» BM25-режим, а деградация.

## Проверка

Без базы и модели (unit):

```powershell
go test -count=1 ./internal/search ./internal/api ./cmd/server ./cmd/index-sources ./internal/store/...
```

Живой контур: PostgreSQL с векторными миграциями + Ollama + модель:

```powershell
$env:VECTOR_TEST_DATABASE_URL = $env:DATABASE_URL
$env:INGEST_TEST_DATABASE_URL = $env:DATABASE_URL
$env:OLLAMA_TEST_URL = "http://127.0.0.1:11434"
$env:OLLAMA_EMBED_MODEL = "nomic-embed-text"
go test -v -count=1 ./tests/database -run TestСемантическийПоиск
go test -v -count=1 ./internal/search -run TestНастоящаяOllama
```

`TestСемантическийПоискНастоящейМоделью` создаёт изолированный run и два
источника, индексирует их настоящей моделью и проверяет, что синонимичный
запрос (без общих словоформ) в режиме `vector` поднимает семантически близкий
источник, а BM25 на нём возвращает 0 строк; после чего проверяет гибридный
режим и BM25-фолбэк при недоступной Ollama.

### Локальный результат проверки (19.11.2026)

Окружение: Windows, Ollama `http://127.0.0.1:11434`, модель
`nomic-embed-text` (768), PostgreSQL 16 + pgvector 0.8.0 в Docker
(`127.0.0.1:5432/activity_descriptor`). Векторные миграции уже были применены
(`goose_vector_versions`: `20261109010000`, `20261109010001`).

- Backfill: 131 источник без векторов → 131 проиндексирован, 0 ошибок
  (`cmd/index-sources`, последовательно, ~0,5–1,5 с на источник).
- В базе после прогона: см. актуальные `count(*)` по `source_fragments` и
  `fragment_embeddings` в отчёте задачи; векторы записаны с
  `model = 'nomic-embed-text'`.
- Живой HTTP-контур: источник, созданный через `POST /api/v1/sources`,
  проиндексирован автоматически; `GET /api/v1/runs/{id}/search` вернул
  `mode=vector` на синонимичном запросе, BM25 на том же запросе — 0 hits
  (подробные значения в отчёте задачи и логе `logs/semantic-index-run.log`).

## Связанные документы

- [VECTOR_STORAGE_GUIDE.md](../migrations_optional/vector/VECTOR_STORAGE_GUIDE.md) —
  схема таблиц, функций и откат миграций.
- [CONFIGURATION.md](CONFIGURATION.md) — группы переменных окружения.
- [docs/agents/PIPELINE_ARCHITECTURE.md](agents/PIPELINE_ARCHITECTURE.md) — место
  RAG в пайплайне финализации.
- [DOCKER_SERVER.md](DOCKER_SERVER.md) — профиль `server` и автоматическое
  применение векторных миграций в Compose.
