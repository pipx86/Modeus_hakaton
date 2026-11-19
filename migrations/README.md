# Инициализация базы Modeus

Для пустой PostgreSQL примените все 27 файлов из `migrations/` в порядке версий Goose. Первый файл, `20261107122603_initial_schema.sql`, создаёт базовую схему и расширение `pgcrypto`. Текущая последняя версия — `20261120000001_run_completed_email_payload.sql`. Goose исполняет секции `Up`, хранит применённые версии в `goose_db_version` и при повторном `up` пропускает уже применённые миграции.

Миграция `20261120000000_auth_users.sql` добавляет роли `admin` и `user`. Первая регистрация через API создаёт администратора. Учётная запись, вакансии, программы, таксономии и демоданные для запуска API не требуются. SQL, который переносит или проверяет ранее сохранённые данные, уже входит в основные миграции.

## Запуск на Windows

1. Запустите PostgreSQL и создайте пустую базу либо поднимите сервис `docker compose up -d --wait postgres`. Роль мигратора должна иметь право создавать объекты в целевой схеме и устанавливать `pgcrypto`. Если расширения управляются отдельно, администратор PostgreSQL может заранее установить `pgcrypto` в этой базе.
2. Из корня репозитория примените основные миграции установленным Goose (проверено с v3.29.0). Для локальной PostgreSQL из Compose с настройками по умолчанию:

   ```powershell
   $env:DATABASE_URL = 'postgres://activity:activity_dev_password@127.0.0.1:5432/activity_descriptor?sslmode=disable'
   goose -dir migrations postgres $env:DATABASE_URL up
   goose -dir migrations postgres $env:DATABASE_URL status
   ```

   Для отдельной базы замените адрес и реквизиты. Продолжайте после успешного выполнения `up`.

3. Запустите процесс API. В примере используется временный локальный ключ и путь к журналу вне репозитория:

   ```powershell
   $env:JWT_SECRET = ([Guid]::NewGuid().ToString('N') + [Guid]::NewGuid().ToString('N'))
   $env:SOURCE_AUTO_INDEX = 'false'
   $env:LOG_FILE = Join-Path ([IO.Path]::GetTempPath()) 'modeus-init.log'
   .\scripts\run-server.ps1 -Port 8080
   ```

   Скрипт читает `.env`, затем `.secrets/llm.env`, затем файлы из `-EnvFiles`. Значения из этих файлов имеют приоритет над окружением оболочки. Для отдельной тестовой базы укажите её настройки в последнем файле с помощью `-EnvFiles`, если локальные файлы задают другой адрес. `cmd/server` проверяет подключение и наличие обязательных объектов схемы, а миграции запускает Goose. Демо-сервер `cmd/api` использует хранилище в памяти.

Для проверки из другой оболочки на одноразовой базе зарегистрируйте первого пользователя. Ожидается HTTP `201` и роль `admin`. Это проверяет чтение ролей и запись в `users`.

```powershell
$body = @{
    email = 'init-check@example.invalid'
    display_name = 'Init Check'
    password = [Guid]::NewGuid().ToString('N')
} | ConvertTo-Json -Compress
$registration = Invoke-WebRequest -Uri 'http://127.0.0.1:8080/api/v1/auth/register' `
    -Method Post -ContentType 'application/json' -Body $body -UseBasicParsing
$registration.StatusCode
($registration.Content | ConvertFrom-Json).user.role
```

Повторите `goose -dir migrations postgres $env:DATABASE_URL up` для той же базы. Goose должен сообщить, что неприменённых миграций нет.

## Поиск по фрагментам и запуск в Compose

Для поиска по фрагментам установите pgvector на сервере PostgreSQL и после основных миграций примените оба файла из `migrations_optional/vector/`:

```powershell
goose -table goose_vector_versions -dir migrations_optional/vector postgres $env:DATABASE_URL up
```

`20261109010000_vector_retrieval.sql` включает `vector` и создаёт хранилище фрагментов, векторов и HNSW-индекс. `20261109010001_lexical_fallback.sql` добавляет BM25 на тех же фрагментах. Для расчёта векторов нужна настроенная модель эмбеддингов. При запуске без поисковой схемы задайте `SOURCE_AUTO_INDEX=false`, чтобы отключить фоновую индексацию новых источников. Остальные настройки описаны в [руководстве по поиску](../migrations_optional/vector/VECTOR_STORAGE_GUIDE.md).

Штатный профиль `server` всегда применяет обе группы. После настройки `JWT_SECRET` в локальном `.env` или `.secrets/llm.env` запустите:

```powershell
docker compose --profile server up -d --build server
```

Compose ждёт готовности `postgres`, затем запускает `migrate` и `migrate-vector` и стартует сервер после успешного завершения этих двух контейнеров. Для запуска интерфейса добавьте `web-server` в конец команды. Параметры подключения описаны в [руководстве по Compose](../docs/DOCKER_SERVER.md).

## Назначение `db/queries/`

`sqlc.yaml` берёт структуру таблиц из `migrations/` и именованные операции из `db/queries/`, чтобы сгенерировать Go-методы в `internal/store/db`. SQL из `db/queries/` выполняется по вызову этих методов. В цепочке Goose файлы из этой папки не участвуют.

| Файлы | Кто использует и для чего |
| --- | --- |
| `runs.sql`, `nodes.sql`, `evidence.sql`, `snapshots.sql` | Бэкенд и хранилище дерева. Статусы исследований, узлы, подтверждения и снимки дерева. |
| `chat_messages.sql`, `chat_sessions.sql` | Хранилище истории диалога и восстановление сессий, в том числе при старте сервера. |
| `sources.sql`, `run_sources.sql`, `file_import_results.sql` | Бэкенд и импорт. Операции с источниками, связи с исследованиями и сохранение результатов загрузки. |
| `vacancy_catalog.sql`, `skill_history.sql`, `academic.sql`, `publication_subscriptions.sql` | Прикладные запросы каталога вакансий, истории навыков, образовательных программ и подписок на публикации. |
| `demo_fixture.sql` | Синтетические данные по явному запуску `go run ./cmd/demo-fixture` (`make seed`). Команда используется и в проверках БД. |
| `data_quality.sql` | Аудит дерева по явному запуску `go run ./cmd/data-audit` (`make check-data`). Используется и в проверках БД. Для отчётов вне репозитория задайте `DATA_REPORTS_DIR`. |

Отдельных SQL-файлов только для тестов в этой папке нет. Тесты могут использовать те же сгенерированные запросы.

## Откат последней миграции

Выполняйте эту проверку на отдельной тестовой базе:

```powershell
goose -dir migrations postgres $env:DATABASE_URL down
goose -dir migrations postgres $env:DATABASE_URL up
goose -dir migrations postgres $env:DATABASE_URL up
```

Команда `down` исполняет секцию `Down` последней миграции. Она может удалить данные. Перед откатом в базе с нужными данными сделайте резервную копию. Второй `up` должен сообщить, что миграций для применения нет.

## Проверки дерева и доставка события

- Уникальный индекс `(parent_id, title)` запрещает точные повторы названий у одного ненулевого родителя. Дополнительные отложенные проверки требуют корректного корня для каждого непустого дерева, учитывают пробелы по краям названия и проверяют наличие подтверждений. Одинаковые названия в разных исследованиях допустимы.
- При переходе `runs.status` в `done` триггер записывает событие `run.completed` в очередь `agent_messages`. Уникальный индекс не позволяет создать второе событие `run.completed` для того же исследования.
- Миграция сама не вычисляет HMAC и не выполняет HTTP-запрос. Отдельный процесс `cmd/webhook-worker` читает очередь, подписывает и отправляет событие. Выбранный формат описан в `internal/webhook/README.md`; реальный n8n и запуск обработчика вместе с приложением ещё нужно проверить.
