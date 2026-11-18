# HTTP API и OpenAPI-спецификация

Документ фиксирует, где живёт контракт HTTP API и что изменилось в спецификации
после добавления сравнения снимков, очереди экспертной проверки, отклонения
подтверждений и экспорта завершённого исследования. Полный актуальный список
операций — в спецификации и в Swagger UI.

## Где лежит контракт

| Файл | Назначение |
| --- | --- |
| [`openapi.yaml`](../openapi.yaml) | Каноническая спецификация (OpenAPI 3.0.3), source of truth. |
| [`api-docs/swagger.json`](../api-docs/swagger.json) | Та же спецификация в JSON; именно её загружает Swagger UI (`/swagger/swagger.json`). |
| [`api-docs/swagger.yaml`](../api-docs/swagger.yaml) | Та же спецификация в YAML (`/swagger/swagger.yaml`). |
| [`api-docs/index.html`](../api-docs/index.html) | Статическая страница Swagger UI; роутер монтирует каталог в `/swagger/` как публичный путь. |

Файлы в `api-docs/` — сгенерированные копии канонической спецификации, руками их
не правят. Регенерация (из корня репозитория, после сохранения скрипта в файл):

```python
# gen_served_spec.py
import json
import yaml

spec = yaml.safe_load(open("openapi.yaml", encoding="utf-8"))
with open("api-docs/swagger.json", "w", encoding="utf-8", newline="\n") as file:
    file.write(json.dumps(spec, ensure_ascii=False, indent=2) + "\n")
with open("api-docs/swagger.yaml", "w", encoding="utf-8", newline="\n") as file:
    file.write(yaml.safe_dump(spec, allow_unicode=True, sort_keys=False))
```

## Авторизация, роли и ошибки

- Все `/api/v1/*`, кроме `GET /api/v1/health`, требуют заголовок
  `Authorization: Bearer <JWT>`; `role` claim — `admin`, `teacher`, `rop`,
  `student` или `guest`. `401` — токен отсутствует или невалиден, `403` — роль не
  разрешена для операции. Для GET-роутов демо-данных гость (guest) имеет доступ
  только на чтение; изменяющие операции гостю запрещены.
- `Idempotency-Key` (необязательный заголовок для не-GET запросов): повтор с тем
  же ключом и телом возвращает сохранённый ответ; тот же ключ с другим телом —
  `409 IDEMPOTENCY_CONFLICT`.
- Ошибки — `{"code": "...", "message": "..."}` со стабильным машинным `code`.
  Ошибки валидации дерева/подтверждений отвечают `422` схемой
  `TreeValidationError` (`code`, `node_id`, `details`).
- Операции, которым нужен PostgreSQL, на in-memory демо-стенде отвечают `501
  NOT_IMPLEMENTED`; для его отличия от `403` (нет прав) это отражено в
  спецификации.

## Что актуализировано в этом обновлении

Каноническая спецификация теперь покрывает все маршруты, зарегистрированные в
[`internal/api/handlers.go`](../internal/api/handlers.go): 25 путей и 33 операции.

| Метод и путь | Назначение | Роли | Основные ответы |
| --- | --- | --- | --- |
| `GET /runs/{id}/snapshots/{snapshot_id}/diff?against=` | Diff двух снимков дерева: `added`/`removed`/`changed` + `summary`; узлы и evidence с `source_id`/`ref_id`/`quote` | admin, teacher, rop, guest | 200, 400, 404, 409 `SNAPSHOT_RUN_MISMATCH`, 501 |
| `GET /reviews/evidence?status=&limit=&offset=` | Очередь спорных подтверждений для эксперта: `status=pending\|rejected`, `limit` 1..200 (50), `offset` >= 0; пустая страница — `[]` | admin, teacher, rop | 200, 400, 501 |
| `POST /evidences/{id}/reject` | Отклонить подтверждение (только pending) | admin, teacher | 204, 404, 409 `ALREADY_REJECTED`/`ALREADY_APPROVED` |
| `GET /evidences/{id}` | Подтверждение + legacy-флаг `approved` | admin, teacher, rop, guest | 200, 404, 501 |
| `POST /evidences/bulk-approve` | Массовое подтверждение, `{approved, skipped}` | admin, teacher | 200, 400 |
| `GET /runs/{id}/messages` | История чата интервью | все роли | 200, 404, 503 `CHAT_DISABLED` |
| `POST /runs/{id}/messages` | Ответ интервьюируемого | admin, teacher, rop, student | 201, 400, 404, 409, 503 |
| `DELETE /runs/{id}` | Удаление исследования (каскадно) | admin | 204, 404 |
| `GET /users` | Демо-пользователи и роли | admin | 200, 403 |
| `GET /runs/{id}/conflicts` | Разрешённые конфликты синтезатора | admin, teacher, rop | 200, 404, 501 |
| `GET /runs/{id}/agent-messages` | Память агента-синтезатора (`payload` с конфликтом) | admin, teacher, rop | 200, 404, 501 |
| `GET /runs/{id}/export?format=md\|json` | Экспорт завершённого исследования: Markdown-отчёт (по умолчанию) или структурированный JSON (`schema=run-export/v1`) | admin, teacher, rop, guest | 200, 400, 404, 409 `RUN_NOT_FINISHED`/`RUN_HAS_NO_TREE`, 500, 503, 504 |

Детали контрактов: [`snapshot_diff.go`](../internal/api/snapshot_diff.go),
[`review_queue.go`](../internal/api/review_queue.go),
[`synthesis_conflicts.go`](../internal/api/synthesis_conflicts.go),
[`export.go`](../internal/api/export.go).

Остальные операции (health, sources, evidences CRUD, runs, tree, answers,
events, snapshots, search, index, nodes) сохранили прежнее поведение; в
спецификацию добавлены недостающие коды ответов и схемы полей, которые реально
отдают Go-модели (`Evidence.status`, `Node.zun`, `Run.type`, `RunStatus.degraded`,
`Source.source_url`), а также примеры запросов/ответов.

## Экспорт исследования

`GET /runs/{id}/export` отдаёт результат завершённого (`status=done`) прогона.

- `format=md` (по умолчанию) — Markdown-отчёт: профессия, статус, run id,
  иерархия `role → function → task → action`, таблица подтверждений (цитата,
  `source_id`, `ref_id`, статус), разрешённые конфликты, финальное сообщение чата
  с рекомендациями/курсами (если доступно) и время генерации.
- `format=json` — документ `schema=run-export/v1`: `run`, вложенное `tree`,
  плоский `nodes`, `evidence`, `conflicts`, `final_message`, `generated_at` и
  (при наличии PostgreSQL) метаданные `sources`. Полный `content` источников
  намеренно не экспортируется.
- Любое другое значение `format` — `400 BAD_REQUEST`; формат проверяется до
  поиска прогона.
- Неизвестный/невалидный `id` — `404 NOT_FOUND`; незавершённый прогон —
  `409 RUN_NOT_FINISHED`; завершённый прогон без сохранённого дерева —
  `409 RUN_HAS_NO_TREE`.
- Роли: `admin`, `teacher`, `rop`, `guest`; `student` получает `403` (как и
  раньше на этом маршруте), без токена — `401`.
- `Content-Type` — `text/markdown; charset=utf-8` или
  `application/json; charset=utf-8`; `Content-Disposition: attachment` с
  безопасным именем `run-<sanitized-id>.md|.json` (только ASCII-буквы, цифры,
  `-`, `_`).
- Конфликты и метаданные источников читаются best-effort: их ошибка логируется,
  но экспорт основного результата не ломает. Финальное сообщение берётся из
  in-memory чата, поэтому для batch-прогонов и после перезапуска сервера оно
  пустое.

> Изменение контракта: раньше `GET /runs/{id}/export` возвращал «голый» массив
> `domain.Node`. Теперь по умолчанию отдаётся Markdown, а JSON-формат — документ
> `run-export/v1` (узлы доступны в `tree`/`nodes`). Клиентский экспорт во
> фронтенде (`frontend/src/lib/export-tree.ts`) собирается в браузере и от этого
> маршрута не зависит.

## Проверка спецификации

```powershell
# 1. YAML/JSON парсятся, а served-копии совпадают с канонической спецификацией
python -c "import json,yaml; o=yaml.safe_load(open('openapi.yaml',encoding='utf-8')); j=json.load(open('api-docs/swagger.json',encoding='utf-8')); y=yaml.safe_load(open('api-docs/swagger.yaml',encoding='utf-8')); print('json==source:', j==o, '; yaml==source:', y==o)"

# 2. Линтер OpenAPI 3 из frontend/node_modules (ошибок быть не должно)
cd frontend
$env:NODE_PATH = (Resolve-Path node_modules).Path
node -e "const {createConfig,lint}=require('@redocly/openapi-core');(async()=>{const c=await createConfig({extends:['recommended']});const p=await lint({ref:'../openapi.yaml',config:c});for(const x of p)console.log(x.severity,x.ruleId,x.message);process.exit(p.some(x=>x.severity==='error')?1:0)})()"

# 3. Спецификация пригодна для генерации типов frontend
npx --no-install openapi-typescript ../openapi.yaml -o schema-check.d.ts
```

Известное ожидаемое предупреждение линтера: у публичного `GET /health` нет 4xx
ответа (это корректно) и в `info` не указана лицензия.

## Известные расхождения (для владельца кода)

- [`frontend/openapi.yaml`](../frontend/openapi.yaml) и сгенерированный
  `frontend/src/lib/api/schema.d.ts` — отдельная копия контракта; она расходится с
  корневой спецификацией: содержит `PATCH /evidences/{id}` (в Go-роутере не
  зарегистрирован — вернётся 405), поля `Evidence.confidence`,
  `ChatMessage.citations`, `Run.degraded_reason/error_message`, которых Go-модели
  не отдают, и `type`/`parent_id` в `PATCH /nodes/{id}`, которые бэкенд
  принимает, но игнорирует. Синхронизацию нужно проводить отдельно (см. README).
- `postman/specs/Activity Descriptor API/swagger.json` — устаревшая Swagger 2.0
  копия, не обновлялась вместе с контрактом.
- `internal/api/contract_routes_test.go` (`demoContractRoutes`) перечисляет не все
  зарегистрированные маршруты (нет `reject`, `bulk-approve`, `GET /evidences/{id}`,
  `messages`, `DELETE /runs/{id}`, `users`, `conflicts`, `agent-messages`).
- `PUT /runs/{id}/tree`: документирован `409 TREE_ALREADY_EXISTS`, но
  `handlePutTree` не маппит `ErrTreeExists` (через `mapStoreError` он уходит в
  500); при повторном сохранении дерева ожидаемый код — 409.
