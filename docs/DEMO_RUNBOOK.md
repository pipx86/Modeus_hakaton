# Демо-день: предполётная проверка и demo-данные

Документ описывает инструменты подготовки к демонстрации Activity Descriptor:

| Инструмент | Назначение |
| --- | --- |
| [`scripts/demo_preflight.py`](../scripts/demo_preflight.py) | Быстрая проверка всего стенда и опциональный полный smoke-прогон интервью → анализ → синтез через HTTP API. |
| [`scripts/demo_data.py`](../scripts/demo_data.py) | Безопасная инспекция прогонов и очистка demo-БД: по умолчанию только отчёт, удаление — только с явными флагами и `--yes`. |
| [`scripts/test_demo_tools.py`](../scripts/test_demo_tools.py) | Stdlib-тесты инструментов (без сети и без живого стенда). |

Оба скрипта используют только стандартную библиотеку Python (`urllib`, `json`,
`argparse`, `subprocess`, `time`) и работают на Windows, Linux и macOS. Они не
печатают JWT и секреты и не читают секреты из командной строки. В примерах ниже
используется `python`; на Linux/macOS при необходимости замените его на `python3`.

## 1. Что должно быть запущено

| Сервис | URL проверки | Ожидаемый ответ |
| --- | --- | --- |
| Backend API | `http://127.0.0.1:8080/api/v1/health` | `200 {"status":"ok"}` |
| Frontend (Vite) | `http://127.0.0.1:5173/` | `200`, страница с `#root` |
| PostgreSQL | `127.0.0.1:5432` | контейнер `activity-descriptor-postgres-1` в статусе `Up (healthy)` |
| Prometheus | `http://127.0.0.1:9090/-/ready` | `200` «Ready» |
| Grafana | `http://127.0.0.1:3000/api/health` | `200`, `"database":"ok"` |
| Jaeger | `http://127.0.0.1:16686/` | `200`, Jaeger UI |

```powershell
# из корня репозитория (backend требует DATABASE_URL и JWT_SECRET)
go run ./cmd/server

# в отдельном терминале — frontend
cd frontend
npm run dev -- --host 127.0.0.1 --port 5173

# PostgreSQL + мониторинг (профиль monitoring)
docker compose -f docker-compose.yml up -d postgres
docker compose -f docker-compose.yml --profile monitoring up -d prometheus grafana jaeger
```

Переменные LLM (`LLM_BASE_URL`, `LLM_API_KEY`, модели) описаны в
[`docs/CONFIGURATION.md`](CONFIGURATION.md) и должны быть такими же, как у
запущенного backend: preflight проверяет их наличие, но не значения.

## 2. Токен и секреты

Все `/api/v1/*`, кроме `/health`, требуют `Authorization: Bearer <JWT>`.
Скрипты берут токен в таком порядке:

1. `--token <JWT>`;
2. переменная окружения `DEMO_TOKEN` или `API_TOKEN`;
3. подпись JWT из `JWT_SECRET` (переменная окружения или `.env`) — тем же
   секретом, что использует backend. Скрипт делает токен роли `admin` сам и
   нигде его не печатает.

`JWT_SECRET` — то же значение, с которым запущен backend. Не коммитьте его:
`.env` уже в `.gitignore`, а в документацию и логи попадают только факты
«переменная задана/не задана».

```powershell
# PowerShell: тот же секрет, что у backend (значение не коммитить)
$env:JWT_SECRET = '<JWT_SECRET backend>'
python scripts/demo_preflight.py
```

```bash
# bash/zsh
export JWT_SECRET='<JWT_SECRET backend>'
python scripts/demo_preflight.py
```

Альтернатива — заранее выпущенный токен:

```powershell
# выпустить admin-JWT и передать его через DEMO_TOKEN
$env:DEMO_TOKEN = (go run ./cmd/local-token -role admin)
python scripts/demo_preflight.py

# или явно
python scripts/demo_preflight.py --token "$env:DEMO_TOKEN"
```

Для ручной проверки UI используйте `go run ./cmd/test-users -json`: он печатает
admin и guest токены, которые frontend читает из `VITE_API_TOKEN`. На стенде
guest — только чтение, для `demo_data.py --cleanup-failed --yes` нужен admin.

## 3. Быстрая проверка стенда

```powershell
python scripts/demo_preflight.py
```

Что проверяется:

- backend `/api/v1/health` и `/metrics` (метрики `activity_descriptor_*`);
- frontend, Prometheus, Grafana, Jaeger — доступность и «осмысленный» ответ;
- PostgreSQL через backend: `GET /api/v1/runs` (плюс необязательная проверка
  контейнера через `docker ps`, если Docker CLI доступен);
- выпуск и приём JWT: `GET /api/v1/users` с токеном admin;
- наличие LLM-конфигурации (`LLM_BASE_URL`/`LLM_API_KEY` в окружении или `.env`,
  либо живые вызовы interviewer-модели в `/metrics`) — без вывода секретов;
- хотя бы один прогон со статусом `done` (печатается его id), непустое дерево
  этого прогона, снимки и очередь экспертной проверки `/reviews/evidence`.

Коды выхода: `0` — все проверки прошли (предупреждения допускаются), `1` — есть
хотя бы один FAIL, `2` — некорректные аргументы. При FAIL в конце печатается
блок `ACTION REQUIRED` с конкретным действием.

Полезные флаги:

| Флаг | Смысл |
| --- | --- |
| `--json` | один JSON-отчёт в stdout (удобно для автоматизации и CI); прогресс `--full` уходит в stderr |
| `--base-url URL` | другой адрес backend (по умолчанию `http://127.0.0.1:8080`, можно задать `DEMO_API_URL`) |
| `--frontend-url`, `--prometheus-url`, `--grafana-url`, `--jaeger-url` | переопределить адреса сервисов |
| `--skip-docker` | не вызывать `docker ps`, проверять PostgreSQL только через API |
| `--http-timeout SECONDS` | таймаут одного HTTP-запроса (по умолчанию 20) |

Пример успешного quick-отчёта (стенд 18.11.2026):

```text
[PASS] backend health          GET /api/v1/health -> 200 {"status":"ok"}
[PASS] backend metrics         GET /metrics -> 200 (activity_descriptor_* exposed)
[PASS] jwt issuance            token source: JWT_SECRET; GET /api/v1/users -> 200 (admin token accepted)
[PASS] postgresql (via /api/v1/runs)  GET /api/v1/runs -> 200, N run(s)
[PASS] completed run           3ef3de34-8438-437d-8f77-3fe2a7c6ce4f (Data Analyst, ...)
RESULT: 14 passed, 0 warning(s), 0 failed
verdict: READY for the demo
```

## 4. Полный smoke-прогон `--full`

```powershell
python scripts/demo_preflight.py --full
```

Скрипт сначала выполняет quick-проверки, затем:

1. создаёт прогон `POST /api/v1/runs` (по умолчанию `profession="Data Analyst"`);
2. отвечает на 6 сценарных ответов через `POST /api/v1/runs/{id}/messages`,
   дожидаясь каждого следующего вопроса интервьюера;
3. ждёт завершения анализа и синтеза и проверяет:
   - `status=done` у прогона;
   - `GET /runs/{id}/tree` возвращает больше 0 узлов;
   - последнее сообщение агента содержит ссылки Stepik (для поддерживаемых
     профессий) — это финальное сообщение интервьюера;
   - `GET /runs/{id}/snapshots` содержит хотя бы один автоматический снимок;
   - `GET /runs/{id}/conflicts` отвечает `200` (число разрешённых конфликтов
     печатается).

Успешный прогон **не удаляется**: он остаётся запасным demo-прогоном, а скрипт
печатает его id. Длительность — обычно 2–10 минут и зависит от провайдера LLM.

```powershell
# больше времени на медленный провайдер и другая профессия
python scripts/demo_preflight.py --full --timeout 1200 --profession "Backend Developer"

# машиночитаемый результат: run_id, status, tree_nodes, snapshots, final_stepik
python scripts/demo_preflight.py --full --json
```

Важные флаги:

| Флаг | Смысл |
| --- | --- |
| `--timeout SECONDS` | общий таймаут пайплайна после создания прогона (по умолчанию 900) |
| `--answer-timeout SECONDS` | сколько ждать один вопрос интервьюера (по умолчанию 240) |
| `--poll SECONDS` | интервал опроса API (по умолчанию 2) |
| `--profession NAME` | профессия для smoke-прогона (по умолчанию `Data Analyst`) |
| `--force` | запустить `--full`, даже если не видно LLM-конфигурации (без LLM пайплайн завершится без дерева) |

Перед созданием прогона скрипт отказывается работать, если не видит LLM ни в
окружении/`.env`, ни в метриках backend: иначе получится статус `done` без дерева
и снимков. На старте `--full` без LLM используйте `--force` только осознанно.

Если провайдер ответил таймаутом, скрипт завершается с кодом `1`, печатает run id
и оставляет прогон для разбора. Причина видна в `logs/app.log` (строки
`finalizer: analyze:` / `provider timeout`). Повторите `--full` позже, а
неудачный прогон уберите через `demo_data.py` (см. ниже).

## 5. Инспекция demo-данных

По умолчанию `demo_data.py` **ничего не меняет** — печатает отчёт:

```powershell
python scripts/demo_data.py
```

Отчёт содержит:

- сводку `Runs: N total | done=… failed=… running=… degraded=… pending=…`;
- таблицу прогонов: `ID`, `STATUS`, `PROFESSION`, `STAGE`, `STARTED`, `AGE`,
  `NODES` (число узлов дерева запрашивается для завершённых прогонов);
- отдельный блок `FAILED RUNS` — кандидаты на очистку;
- опциональный блок `KEEP NEWEST N COMPLETED RUN(S)`.

Фильтры и режимы:

```powershell
# только неудачные прогоны
python scripts/demo_data.py --status failed

# прогоны старше недели + показать 3 новейших завершённых (только отчёт)
python scripts/demo_data.py --older-than 7 --keep-completed 3

# машиночитаемый отчёт: сводка, прогоны, кандидаты, keep-completed
python scripts/demo_data.py --json

# быстро, без запросов дерева по каждому прогону
python scripts/demo_data.py --no-nodes
```

Колонка `NODES` показывает `?`, если дерево недоступно (например, у
незавершённого прогона). Данные берутся из `GET /api/v1/runs` и
`GET /api/v1/runs/{id}/tree`; секреты не печатаются.

## 6. Безопасная очистка demo-данных

Правила безопасности встроены в скрипт:

- без `--cleanup-failed` или `--delete-run` любые действия невозможны;
- без `--yes` выполняется только dry-run с планом удаления;
- `--cleanup-failed` удаляет **только** прогоны со статусом `failed`; завершённые
  (`done`/`degraded`) и запущенные (`running`) прогоны в кандидаты не попадают;
- `--delete-run` удаляет только явно перечисленные id; для завершённых прогонов
  дополнительно требуется `--allow-completed` (защита от случайного удаления
  demo-прогонов);
- `--keep-completed N` — только отчёт, ничего не удаляет;
- `--older-than DAYS` сужает и отчёт, и кандидатов на очистку.

Сначала посмотрите план (ничего не удалится):

```powershell
python scripts/demo_data.py --cleanup-failed
```

Затем удалите только failed-прогоны:

```powershell
# осознанное подтверждение: --yes
python scripts/demo_data.py --cleanup-failed --yes

# только failed-прогоны старше 3 дней
python scripts/demo_data.py --cleanup-failed --older-than 3 --yes
```

Точечное удаление конкретных id:

```powershell
# dry-run конкретного id (id берётся из отчёта)
python scripts/demo_data.py --delete-run <run-id>

# удалить именно этот прогон
python scripts/demo_data.py --delete-run <run-id> --yes

# удалить завершённый прогон можно только с дополнительным подтверждением
python scripts/demo_data.py --delete-run <run-id> --allow-completed --yes
```

`--cleanup-failed` не трогает прогоны в статусе `running`. Если прогон «завис» в
`running` на часы, убедитесь, что backend его не обрабатывает, и удалите явно:
`python scripts/demo_data.py --delete-run <run-id> --yes`.

После очистки повторите отчёт и `python scripts/demo_preflight.py`: должен
остаться минимум один завершённый прогон с непустым деревом (лучше — последний
успешный `--full`).

## 7. Чеклист демо-дня

**За 30 минут до демо**

- [ ] Запущены все сервисы: backend, frontend, PostgreSQL, Prometheus, Grafana,
      Jaeger (см. таблицу в разделе 1).
- [ ] `python scripts/demo_preflight.py` — `RESULT: … 0 failed` и
      `verdict: READY for the demo`.
- [ ] `python scripts/test_demo_tools.py` — `OK` (проверка самих инструментов).
- [ ] `python scripts/demo_data.py` — есть `done`-прогон с непустым деревом; нет
      «свежих» `failed` от экспериментов.
- [ ] `python scripts/demo_data.py --cleanup-failed --yes` — убрать failed-прогоны
      (сначала посмотреть план без `--yes`).
- [ ] Один успешный `python scripts/demo_preflight.py --full` — записать его
      `run_id` как запасной demo-прогон. Этот прогон не удалять.
- [ ] Проверить вручную: UI с `VITE_API_TOKEN` (admin), дашборд Grafana, таргеты
      Prometheus, Jaeger UI, очередь `/api/v1/reviews/evidence`.
- [ ] Проверить, что `logs/app.log` не растёт ошибками `finalizer`/`provider`.

**Во время демо**

- [ ] Quick-проверка перед показом: `python scripts/demo_preflight.py`.
- [ ] Если LLM медленный/недоступен — показывать записанный ранее `done`-прогон,
      а не запускать новый pipeline.
- [ ] Не запускать `demo_data.py --cleanup-failed --yes` во время демо.
- [ ] При сбое backend смотреть `logs/app.log`; после рестарта backend in-memory
      чат-сессии теряются, но деревья и снимки завершённых прогонов остаются в
      PostgreSQL.

**После демо**

- [ ] Сохранить последний успешный прогон как backup (он не удаляется
      `--cleanup-failed`).
- [ ] Убрать только failed-прогоны, созданные при показах:
      `python scripts/demo_data.py --cleanup-failed --yes`.

## 8. Типичные проблемы

| Симптом | Что проверить | Действие |
| --- | --- | --- |
| `backend health` FAIL | процесс backend, `DATABASE_URL`, `JWT_SECRET` | `go run ./cmd/server`; смотреть `logs/app.log` |
| `jwt issuance` 401/403 | `JWT_SECRET` скрипта ≠ backend; роль токена | задать тот же `JWT_SECRET`, перевыпустить `go run ./cmd/local-token -role admin` |
| `postgresql (via /api/v1/runs)` 500 | контейнер PostgreSQL, миграции | `docker ps`; `docker compose -f docker-compose.yml up -d postgres`; применить миграции |
| `frontend` FAIL | Vite dev server/порт 5173 | `cd frontend; npm run dev -- --host 127.0.0.1 --port 5173` |
| `prometheus`/`grafana`/`jaeger` FAIL | профиль monitoring | `docker compose -f docker-compose.yml --profile monitoring up -d prometheus grafana jaeger` |
| `llm configuration` WARN | в окружении нет `LLM_*`, в `/metrics` нет вызовов моделей | экспортировать те же `LLM_*`, что у backend; либо `--full --force` (дерева не будет) |
| `--full`: `status=failed` | `logs/app.log`: `finalizer: analyze: … timeout` | повторить позже; провайдер был недоступен; failed-прогон убрать через `demo_data.py` |
| `snapshots` WARN | снимки появились только у новых прогонов | выполнить `--full`, чтобы получить прогон со снимком |
| `review queue` 501 | backend запущен без PostgreSQL | запустить `cmd/server` с `DATABASE_URL` |
| `messages` 404 после рестарта backend | in-memory чат-сессия пропала | это ожидаемо; дерево/снимки прогона сохраняются |
| `demo_data.py` удалил не всё | у прогона не статус `failed` | завершённые удаляются только явным `--delete-run … --allow-completed --yes` |
| в отчёте есть «вечный» `running` | backend не обрабатывает этот прогон | удалить явно: `--delete-run <id> --yes`; `--cleanup-failed` его не тронет |
| `demo_data.py` 401/403 | токен не admin | `go run ./cmd/local-token -role admin`, передать через `DEMO_TOKEN` или `--token` |
| ничего не удалилось | не передан `--yes` | это dry-run; повторить с `--yes` после проверки плана |

## 9. Секреты и артефакты

- Скрипты не печатают токены и значения `LLM_API_KEY`/`JWT_SECRET`; в отчётах
  только «set/unset».
- Не коммитьте `.env`, токены и дампы из `logs/`; `.env` и `logs/` уже в
  `.gitignore`.
- `--json` можно сохранять в файл для отчёта, но не вкладывайте туда окружение.
- Для проверки инструментов без живого стенда: `python scripts/test_demo_tools.py`.
- Статическая проверка DevOps-ассетов по-прежнему запускается командой
  `python scripts/check_deploy.py`.
