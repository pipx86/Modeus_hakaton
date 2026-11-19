# HTTP API и OpenAPI-спецификация

Документ фиксирует, где живёт контракт HTTP API и что изменилось в спецификации
после добавления сравнения снимков и исследований, очереди экспертной проверки и
её прогресса, отклонения подтверждений, экспорта завершённого исследования,
оценки стоимости прогона, инсайтов/покрытия, Auto-ZUN и контекста подтверждения
в транскрипте интервью. Полный актуальный список операций — в спецификации и в
Swagger UI.

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
[`internal/api/handlers.go`](../internal/api/handlers.go): 37 путей и 46 операций.

| Метод и путь | Назначение | Роли | Основные ответы |
| --- | --- | --- | --- |
| `GET /runs/{id}/tree` | Плоский список узлов **и** детерминированные инсайты: provenance, health score с breakdown, red flags, market heat, coverage, sources и объяснения узлов | admin, teacher, rop, student, guest | 200, 404, 500 |
| `GET /runs/{id}/snapshots/{snapshot_id}/diff?against=` | Diff двух снимков дерева: `added`/`removed`/`changed` + `summary`, человекочитаемые `story`/`story_lines`; узлы и evidence с `source_id`/`ref_id`/`quote` | admin, teacher, rop, student, guest | 200, 400, 404, 409 `SNAPSHOT_RUN_MISMATCH`, 501 |
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
| `POST /runs/{id}/sources` | Привязать standard/vacancy источник к прогону для live multi-source анализа; admin/teacher | 201, 400, 401, 403, 404, 422, 501 |
| `GET /runs/{id}/export?format=json\|md` | Экспорт завершённого исследования: legacy-массив узлов по умолчанию, JSON-документ через `?format=json`, Markdown через `?format=md` | admin, teacher, rop, student, guest | 200, 400, 404, 409 `RUN_NOT_FINISHED`/`RUN_HAS_NO_TREE`, 500, 503, 504 |
| `GET /runs/estimate?profession=&type=` | Оценка длительности/токенов/стоимости будущего прогона по истории; `estimated_cost_usd` может быть `null`, `confidence` — high/medium/low, `method` называет использованные данные | admin, teacher, rop, student | 200, 400, 500 |
| `GET /runs/{id}/cost` | Фактический расход LLM по прогону: токены, стоимость (`null` если провайдер не сообщил цену), `by_model`/`by_agent`, `status_counts`, `attribution`, плюс пересчитанная оценка | все роли (как `GET /runs/{id}`) | 200, 404, 500, 503, 504 |
| `GET /runs/{id}/insights` | Консолидированные инсайты: provenance по узлам, health score с breakdown, red flags, market heat, coverage, sources, node explanations; детерминированно, без LLM | admin, teacher, rop, guest | 200, 401, 403, 404, 500, 501, 503, 504 |
| `GET /runs/{id}/coverage` | Матрица покрытия роль/функция/задача × типы источников; JSON по умолчанию, `?format=csv` — `text/csv` с заголовком `node_id,title,type,interview,standard,vacancy,document` | admin, teacher, rop, guest | 200, 401, 403, 404, 500, 501, 503, 504 |
| `GET /runs/{id}/compare?against=` | Сравнение деревьев двух исследований: `added`/`removed`/`changed`/`summary`, `match` (`by_id`/`by_title`/`unmatched`), `same_profession`, `story` | admin, teacher, rop, guest | 200, 400, 401, 403, 404, 409 `RUN_PROFESSION_MISMATCH`/`RUN_TREE_MISSING`, 500, 501, 503, 504 |
| `GET /runs/{id}/zun/suggestions` | Rule-based предложения ЗУН для каждого узла дерева (`source=rule`, без LLM); у узлов с непустым ЗУН списки пусты и `can_apply=false` | admin, teacher, rop | 200, 401, 403, 404, 500 |
| `POST /runs/{id}/zun/suggestions/apply` | Сохранить предложенный ЗУН одному узлу, только если ЗУН ещё пуст; нормализация пунктов и лимиты | admin, teacher, rop | 200, 400, 401, 403, 404, 409 `ZUN_ALREADY_SET`, 500, 501 |
| `POST /runs/{id}/zun/suggestions/reject` | Постоянно отклонить предложение ЗУН для узла; повторный GET больше его не возвращает, идемпотентно | admin, teacher, rop | 200, 400, 401, 403, 404, 500, 501 |
| `GET /runs/{id}/evidence/{evidence_id}/context` | Контекст подтверждения: цитата с контекстом узла/источника, сохранённый транскрипт интервью и лучшая совпавшая реплика (`match`, `note`) | все роли | 200, 400, 401, 404, 500, 501 |
| `GET /reviews/evidence/progress?run_id=` | Счётчики проверки подтверждений: `pending`/`approved`/`rejected`/`total`, глобально или по прогону (`scope`/`run_id`) | admin, teacher, rop | 200, 400, 401, 403, 404, 500, 501 |

Детали контрактов: [`snapshot_diff.go`](../internal/api/snapshot_diff.go),
[`comparison_story.go`](../internal/api/comparison_story.go),
[`review_queue.go`](../internal/api/review_queue.go),
[`review_progress.go`](../internal/api/review_progress.go),
[`synthesis_conflicts.go`](../internal/api/synthesis_conflicts.go),
[`export.go`](../internal/api/export.go),
[`cost_estimate.go`](../internal/api/cost_estimate.go),
[`insights.go`](../internal/api/insights.go),
[`run_compare.go`](../internal/api/run_compare.go),
[`zun_suggestions.go`](../internal/api/zun_suggestions.go),
[`evidence_context.go`](../internal/api/evidence_context.go).

Остальные операции (health, sources и их загрузка/индексация, evidences CRUD,
approve/reject/bulk-approve, runs, tree, answers, events, messages, snapshots,
search, nodes, users, конфликты синтезатора) сохранили прежнее поведение; в
спецификацию добавлены недостающие коды ответов и схемы полей, которые реально
отдают Go-модели (`Evidence.status`, `Node.zun`, `Run.type`, `RunStatus.degraded`,
`Source.source_url`, `SnapshotDiff.story`/`story_lines`), а также примеры
запросов/ответов.

## Экспорт исследования

`GET /runs/{id}/export` отдаёт результат завершённого (`status=done`) прогона.

- `format=json` — документ `schema=run-export/v1`: `run`, вложенное `tree`,
  плоский `nodes`, `evidence`, `conflicts`, `final_message`, `generated_at` и
  (при наличии PostgreSQL) метаданные `sources`. Полный `content` источников
  намеренно не экспортируется.
- `format=md` — Markdown-отчёт: профессия, статус, run id, иерархия
  `role → function → task → action`, таблица подтверждений (цитата, `source_id`,
  `ref_id`, статус), разрешённые конфликты, финальное сообщение чата с
  рекомендациями/курсами (если доступно) и время генерации.
- Без `format` возвращается legacy-массив `domain.Node` для обратной совместимости; `Accept: text/markdown` включает Markdown.
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

> Примечание: раньше `GET /runs/{id}/export` всегда возвращал «голый» массив
> `domain.Node`. Сейчас без явного `format` по-прежнему отдаётся legacy-массив
> (обратная совместимость); Markdown включается через `?format=md` или
> `Accept: text/markdown`, а подробный JSON-документ — через `?format=json`.
> Клиентский экспорт во фронтенде (`frontend/src/lib/export-tree.ts`) собирается
> в браузере и от этого маршрута не зависит.

## Инсайты и покрытие исследования

`GET /runs/{id}/insights` — один детерминированный payload вместо шести запросов:

- `provenance.nodes[node_id]` — `source_count`, `source_types` (в порядке
  доверия: interview → standard → vacancy → document) и `evidence_count` после
  дедупликации подтверждений; неизвестные `source_id` считаются document.
- `health` — 0..100 score с `breakdown` (groundedness 0.40, completeness 0.30,
  duplicates 0.20, orphans 0.10), `weights`, сырые `counts` и текстом `formula`.
- `flags` — red flags по пяти фиксированным правилам (`SUSPICIOUS_TITLE`,
  `DUPLICATE_TITLE`, `SINGLE_WEAK_SOURCE`, `NO_CHILDREN`, `NO_EVIDENCE`);
  `codes` и `reasons` — параллельные массивы.
- `market_heat` — частоты навыков, наложенные на текст узлов; `source`:
  `run.skill_freq` (сохранённые частоты прогона), `vacancy_sources`
  (детерминированное извлечение из содержимого vacancy/standard-источников,
  вместе с `sample_size`) или `none`; пустые `top`/`nodes` при отсутствии данных.
- `coverage` — та же матрица, что отдаёт `GET /runs/{id}/coverage`.
- `sources` — метаданные источников прогона (`id`, `type`, `title`, `priority`).
- `node_explanations[node_id]` — шаблонный текст «почему этот узел здесь» и
  подтверждения с `source_id`/`source_type`/`priority`/`ref_id`/`status`.

`GET /runs/{id}/coverage` отдаёт `{run_id, columns, rows}`; строки — role/function/
task (actions исключены), `cells` содержит счётчики по всем четырём колонкам
(0, если источников этого типа нет). `?format=csv` (без учёта регистра и
пробелов) отдаёт `text/csv; charset=utf-8` с заголовком
`node_id,title,type,interview,standard,vacancy,document` и
`Content-Disposition: attachment`. Любое другое значение `format` — JSON.

Оба маршрута требуют PostgreSQL и привязанные источники прогона; на in-memory
демо-хранилище возвращается `501 NOT_IMPLEMENTED`. Роли: admin/teacher/rop/guest
(student получает 403). Никаких LLM/внешних вызовов при построении нет.

## Сравнение исследований

`GET /runs/{id}/compare?against={other_run_id}` сравнивает **текущие сохранённые
деревья** двух исследований той же профессии и работает только на чтение.

- Направление как у snapshot diff: `{id}` — голова (head), `against` — база
  (base); `added` — узлы только у head, `removed` — только у базы.
- Матчинг двухпроходный: сначала по `node.id` (как в snapshot diff), затем
  оставшиеся — семантически по `type` + нормализованному `title` + нормализованному
  пути предков; неоднозначные пары не сопоставляются и попадают в
  `added`/`removed`. `match.by_id`, `match.by_title` и `match.unmatched`
  показывают, откуда взялся результат (`unmatched = added + removed`).
- `same_profession` — профессии совпали нормализованно по `runs.profession` или
  по названию корневого role-узла; иначе `409 RUN_PROFESSION_MISMATCH`.
- У обоих прогонов должно быть сохранённое дерево, иначе
  `409 RUN_TREE_MISSING`; статус прогона сравнению не мешает.
- `changed` перечисляет поля в фиксированном порядке
  title → description → type → parent_id → evidence; `story` — детерминированное
  человекочитаемое описание diff'а, количества в котором совпадают с `summary`.
- `against` обязателен, должен быть UUID и отличаться от `id` (иначе 400);
  неизвестный прогон/невалидный UUID в пути — 404; in-memory хранилище — 501.
- Роли: admin/teacher/rop/guest, чтение; student получает 403.

## Оценка стоимости и фактический расход

`GET /runs/estimate?profession=&type=chat|batch` — оценка **до** запуска, только
по уже сохранённым данным (никаких внешних вызовов). `type` и `profession`
обязательны (иначе 400).

- Длительность — медиана времени завершённых прогонов той же профессии и типа;
  при отсутствии — медиана всех прогонов типа, затем документированная константа.
- Токены — среднее атрибутированное использование истории профессии, иначе
  среднее по сохранённым LLM-запросам × ожидаемое число вызовов, иначе константа.
- `estimated_cost_usd` — `null`, если нет записанной цены (или
  `COST_ESTIMATE_USD_PER_1M_TOKENS`), а не выдуманный ноль.
- `confidence` — `high` (≥10 сэмплов длительности и токенов), `medium` (≥3
  обоих), иначе `low`; `sample_size` — число прогонов за оценкой длительности,
  `method` — какие входные данные использованы.

`GET /runs/{id}/cost` отдаёт фактические данные прогона: количество запросов,
prompt/completion/total токены, стоимость (`null` — «неизвестно», не «бесплатно»),
`by_model`, `by_agent`, `status_counts`, длительность (для running-прогона —
прошедшее время) и `estimate` — та же оценка, пересчитанная без этого прогона для
сравнения. `attribution` объясняет, откуда взяты запросы: `run_id` (точная
атрибуция), `time_window` (окно прогона; приблизительно) или `none`;
`attribution_ambiguous=true`, если окно пересекается с другим прогоном.
Роли — как у `GET /runs/{id}` (admin/teacher/rop/student/guest).

## Auto-ZUN (предложения ЗУН)

`GET /runs/{id}/zun/suggestions` — по одному предложению на каждый узел дерева,
строго rule-based (без LLM), `source` всегда `rule`. Узел без ЗУН всегда получает
непустое предложение (правила по тексту узла/цитатам/профессии или fallback по
типу узла); узел с непустым ЗУН не перезаписывается: `knowledge`/`skills`/
`abilities` пусты, `can_apply=false`, `current_zun` содержит сохранённое значение.

`POST /runs/{id}/zun/suggestions/apply` сохраняет ЗУН одному узлу (`node_id`
обязателен), только пока ЗУН пуст; существующий ЗУН не перезаписывается —
`409 ZUN_ALREADY_SET`. Пункты нормализуются (trim, схлопывание пробелов,
дедупликация без учёта регистра, ≤200 символов на пункт, ≤50 пунктов на список);
нужен хотя бы один непустой пункт, иначе 400. Узел должен принадлежать прогону
(404). Роли: admin/teacher/rop; in-memory хранилище тоже умеет apply, для
хранилищ без этой возможности — 501.

## Контекст подтверждения и прогресс проверки

`GET /runs/{id}/evidence/{evidence_id}/context` возвращает подтверждение
(цитату, `ref_id`, названия узла/источника) и сохранённый транскрипт интервью
(до 500 сообщений, по `sequence`), чтобы после перезагрузки страницы проскроллить
чат к нужной реплике и подсветить цитату. `match` выбирается детерминированно:
id сообщения → `answer-N`/`a-N` (N-я реплика пользователя) →
`q-N`/`question-N` (N-й вопрос интервьюера) → `msg-N`/`N` (sequence) → поиск
цитаты в тексте (точный, затем без учёта регистра) → лучшее лексическое
совпадение. `quote_start`/`quote_end` — смещения в рунах, только когда цитата
найдена дословно; при лексическом совпадении их нет. Если сопоставить не удалось,
`match=null`, а `note` объясняет причину. Подтверждение ищется строго внутри
`{id}`: чужое подтверждение неотличимо от несуществующего (404). Роли — все
аутентифицированные; in-memory демо-хранилище отдаёт пустой транскрипт (история
есть только в PostgreSQL или в живом chat-сервисе).

`GET /reviews/evidence/progress[?run_id=]` — лёгкие счётчики для рабочего места
эксперта: `pending`, `approved`, `rejected`, `total`, где `total` всегда равен их
сумме. Без `run_id` счётчики глобальные (`scope=global`), с `run_id` —
`scope=run` и `run_id` в ответе. Невалидный `run_id` — 400, неизвестный — 404.
Роли: admin/teacher/rop; in-memory хранилище счётчики поддерживает, иначе 501.

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

Известные ожидаемые предупреждения линтера: у публичного `GET /health` нет 4xx
ответа (это корректно), в `info` не указана лицензия, а компонент схемы
`RunEvent` пока не используется ни одним ответом (он остаётся в контракте для
генерации frontend-типов).

## Известные расхождения (для владельца кода)

- Типы frontend генерируются из корневого `openapi.yaml` командой
  `npm run generate-api`; отдельная копия `frontend/openapi.yaml` удалена.
- `PATCH /evidences/{id}` зарегистрирован в Go-роутере и описан в контракте.
- `ChatMessage.citations` и `RunEvent` описаны в контракте и приходят из API.
- `Evidence.confidence` и `Run.degraded_reason/error_message` в API отсутствуют:
  `confidence` поддерживается только моками, UI показывает его опционально.
- `PATCH /nodes/{id}` принимает только `title`/`description`; `type` и `parent_id`
  бэкенд игнорирует.
- `postman/specs/Activity Descriptor API/swagger.json` — устаревшая Swagger 2.0
  копия, не обновлялась вместе с контрактом.
- `internal/api/contract_routes_test.go` (`demoContractRoutes`) перечисляет не все
  зарегистрированные маршруты: нет `GET/PATCH /evidences/{id}`, `reject`,
  `bulk-approve`, `POST /sources/upload`, `POST /runs/{id}/sources`, `messages`,
  `DELETE /runs/{id}`, `users`, `GET /runs/estimate`, `cost`, `insights`,
  `coverage`, `compare`, `zun/suggestions`, `zun/suggestions/apply`,
  `evidence/{evidence_id}/context` и `reviews/evidence/progress`.
- `PUT /runs/{id}/tree`: документирован `409 TREE_ALREADY_EXISTS`, но
  `handlePutTree` не маппит `ErrTreeExists` (через `mapStoreError` он уходит в
  500); при повторном сохранении дерева ожидаемый код — 409.
