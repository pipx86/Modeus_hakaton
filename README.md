# Локальный запуск API

Нужны Go версии из go.mod и PostgreSQL. Сервер использует постоянное хранилище PGStore: исследования, дерево и подтверждения сохраняются в БД.

## Подготовка

SQL-миграции находятся в отдельной ветке feature/rustam/goose-migrations. Примените основные миграции до 20261110000000_run_stage.sql включительно. Для поиска дополнительно нужен пакет migrations_optional/vector с pgvector. Миграции в эту ветку бэкенда не переносились.

В PowerShell задайте параметры в текущей консоли:

```powershell
$env:DATABASE_URL = 'postgres://<user>:<password>@127.0.0.1:5432/<database>?sslmode=disable'
$env:JWT_SECRET = '<секрет подписи JWT для локальной разработки>'
$env:HTTP_ADDR = '127.0.0.1:8080'
go run ./cmd/server
```

DATABASE_URL и JWT_SECRET обязательны. HTTP_ADDR по умолчанию — 127.0.0.1:8080. Старый общий API_TOKEN больше не используется: сервер проверяет JWT и роли. Секрет подписи нельзя помещать в Git.

Health доступен без токена: GET /api/v1/health. Он проверяет ответ HTTP-процесса, а не последующую доступность БД.

## Локальный JWT и запрос

В другой консоли с тем же JWT_SECRET можно выпустить короткий JWT для локальной проверки. Это JWT нашего API, он не связан с hh.ru:

```powershell
$env:JWT_SECRET = '<тот же локальный секрет подписи>'
$access = go run ./cmd/local-token -role teacher
$headers = @{ Authorization = "Bearer $access" }
Invoke-RestMethod -Method Post -Uri http://127.0.0.1:8080/api/v1/runs -Headers $headers -ContentType 'application/json' -Body '{"profession":"Аналитик данных"}'
```

Команда local-token рассчитана на разработчика, уже имеющего секрет подписи. Для общего окружения нужен согласованный механизм аутентификации пользователей; HTTP-маршрут свободной выдачи JWT здесь не добавлен.

## Компоненты

- PUT /api/v1/runs/{id}/tree сохраняет новое дерево одной транзакцией; ошибки возвращаются как 422 с code/details.
- POST /api/v1/sources/{id}/index создаёт фрагменты и векторы; GET /api/v1/runs/{id}/search выполняет поиск. strategy=hybrid объединяет ранги BM25 и vector.
- go run ./cmd/webhook-worker отдельно читает очередь run.completed. Нужны DATABASE_URL, WEBHOOK_URL, WEBHOOK_SECRET. Реальный n8n требует настройки принимающей стороны.
- scripts/Configure-Hh.ps1 сохраняет уже действующий токен hh.ru в Windows DPAPI вне проекта. scripts/Import-HhVacancies.ps1 использует его без перевыпуска.

Документация: docs/backend_persistence_guide.md, docs/source_import_guide.md, docs/local_model_profile.md. Контракт маршрутов — openapi.yaml.

## Проверки

Юнит-тесты: go test ./...

Для интеграционных тестов задайте INGEST_TEST_DATABASE_URL отдельной тестовой PostgreSQL. Не используйте рабочую базу.

Воспроизводимый запуск на новом проекте Docker Compose:

```powershell
./scripts/Test-Acceptance.ps1 -DatabaseRepository 'D:\modeus_project\Modeus_hakaton'
```

Скрипт применяет миграции через установленный Goose, проверяет обе рабочие папки в Linux-контейнере и сохраняет логи в data/acceptance/. После проверки удаляется только созданный тестовый проект Compose. Скрипт использует текущие файлы папок, поэтому перед запуском проверьте обе ветки.

Общий сценарий использует настоящие API, PostgreSQL, оркестратор и HTTP-отправщик; шаги агентов и получатель webhook тестовые. Реальные hh.ru и Ollama проверяются отдельно. Завершить сервер можно Ctrl+C.
