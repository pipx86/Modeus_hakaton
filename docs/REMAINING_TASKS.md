# Остаток задач и сверка с фичами наставника

Срез: 19.11.2026, ветка `feature/roman/ai`, после commit `3a49c73`.
Файл — рабочая карта остатка; при изменении статуса обновляйте его, а не держите в переписке.

## 1. Что уже закрыто

- CI: lint, unit/race, acceptance, db-check, migrations, frontend, Docker — зелёные.
- Live chat: interviewer → analyst → deterministic synthesizer → tree → Stepik.
- Live batch: `POST /runs {type:"batch", transcript}` → `domain.Orchestrate` → AI steps → SaveAITree.
- Durable chat: `run_chat_messages`, история переживает рестарт backend.
- Live multi-source: standard/vacancy источники, provenance, конфликты, `GET /runs/{id}/conflicts`.
- Snapshot diff, review queue, export `json`/`md`.
- n8n: webhook → реальный SMTP (Ethereal) и публичный HTTPS (Cloudflare quick tunnel) проверены live; Notion не нужен.
- Monitoring: Grafana без anonymous, Prometheus без DOWN-target, alerts, dashboards.
- Live-eval: `cmd/live-eval`, 2/2 кейса на реальной модели (baseline + standard-conflict).
- Startup: `scripts/run-server.ps1` читает `.env` + `.secrets/llm.env`.

## 2. Незавершённые задачи (наш backlog)

### P0 / перед показом

- [ ] **UI конфликтов**: фронт, вкладка «Конфликты» по `GET /runs/{id}/conflicts`.
  - Данные и live-run `d6193771…` (9 конфликтов) уже есть.
- [ ] **Active SSE**: EventSource не умеет Authorization; фронту нужен `fetch` + `ReadableStream` или dev-прокси с токеном.
  - Durable history завершённых run уже работает; active session — in-memory.
- [ ] **Реальный SMTP**: сейчас live-проверен Ethereal (тестовый ящик), нужен корпоративный SMTP и постоянный получатель.
- [ ] **Постоянный HTTPS webhook**: сейчас Cloudflare quick tunnel (временный URL); нужен домен/постоянный туннель.

### P1 / продукт

- [ ] **Docker full backend**: `Dockerfile` собирает demo `cmd/api`, а не `cmd/server`; нужен `server` service + env_file + миграции.
- [ ] **Batch UI**: фронт создаёт только chat-run; нужен ввод transcript/type=batch или отдельный batch-сценарий.
- [ ] **Production ops**: TLS, Alertmanager, HA, retention/backups, Jaeger persistence, auth для Prometheus/Grafana.
- [ ] **Frontend UX**: guest видит write-кнопки; сырые английские 403; часть состояний чата/источников.
- [ ] **HH app-token refresh**: токен вручную, нужен владелец/процедура обновления.
- [ ] **Multi-source limits**: analyst обрезает источник до ~2000 символов, extra LLM-call на каждый standard/vacancy, conflicts только при distinct title/description.

## 3. Фичи наставника: сверка с текущим кодом

Условные статусы: `готово` / `частично` / `нет`.
«Наше» — то, что уже реализовано или начато в этом репозитории; назначение владельцев из списка наставника сохранено.

| # | Фича | Статус | Что уже есть | Чего не хватает |
|---:|---|---|---|---|
| 1 | Provenance Score (Rustam + Mikhail) | **частично** | evidence хранит `source_id`; finalizer различает interview/standard/vacancy; live multi-source и конфликты | агрегация «число типов источников» в `GET /tree`, поле score, бейдж в `activity-tree.tsx`, тест агрегации |
| 2 | «Почему этот узел здесь» (Mikhail + Egor) | **частично** | диалог узла в `activity-tree.tsx` показывает evidence/цитаты/статус | тип источника, `Source.Priority`, шаблонное обоснование, открытие для любого узла, тест |
| 3 | Tree Health Score (Rustam + Mikhail) | **нет** | — | score 0–100, разбивка groundedness/полнота/дубли/сироты, API/бейдж, docs/тесты |
| 4 | Snapshot Story (Egor + Mikhail) | **частично** | `snapshot_diff.go`, вкладка «Снимки и проверка», added/removed/changed, evidence | человеческий changelog, кнопка копирования, тест пустого diff |
| 5 | Red Flags фильтр (Rustam + Mikhail) | **нет** | — | 5 правил, причина у каждого флага, фильтр в дереве, тест |
| 6 | Evidence → транскрипт (Mikhail + Egor) | **частично** | durable chat (`run_chat_messages`), `ref_id` в evidence/очереди, citations в `ChatTab` | клик по цитате → скролл к сообщению интервью + подсветка, после перезагрузки, тест |
| 7 | Review Progress + клавиатура (Mikhail) | **частично** | review queue и approve/reject, snapshot-review | «подтверждено X из Y», hotkeys approve/reject/next, счётчик из API, тест |
| 8 | Market Heat Overlay (**Роман** + Mikhail) | **частично** | `Run.SkillFreq`, `analysis` считает частоты, backend хранит `skill_freq` | overlay в `activity-tree.tsx`, топ-10, легенда/тултип, маппинг-тест |
| 9 | Coverage Matrix (Rustam + Mikhail) | **нет** | — | `GET /runs/{id}/coverage`, таблица функции×источники, пустые клетки, CSV, тест |
| 10 | Auto-ZUN предложения (**Роман** + Mikhail) | **частично** | `domain.Zun`, `enrichZun` в chat-finalize, показ ЗУН в дереве, PATCH узла | rule-based предложения, accept/reject, защита ручных значений, тест на 10 узлах; batch ZUN не подключён |
| 11 | Run-to-run сравнение (Egor + Mikhail) | **нет** | — | `GET /runs?profession`, diff двух done-прогонов, страница, экспорт, тест |
| 12 | Оценка стоимости/времени (**Роман** + Mikhail) | **частично** | `llm_requests`/`llm_usage`, админ-статистика токенов и latency | оценка до старта в `NewResearch.tsx`, факт после, отклонение, тест |

### Итог по 12 фичам

- полностью готово: **0**;
- частично: **8** (#1, #2, #4, #6, #7, #8, #10, #12);
- отсутствует: **4** (#3, #5, #9, #11).

### Что из этого «наше» (Роман)

Прямые фичи:
- #8 Market Heat Overlay — есть backend `SkillFreq`, нет UI;
- #10 Auto-ZUN — есть auto-enrichment и показ, нет suggestions/accept-reject;
- #12 Cost/Time Estimate — есть post-fact usage, нет pre-run estimate.

Смежное, уже сделанное нами:
- #1 provenance foundation (source types/evidence/conflicts);
- #4 snapshot diff backend + UI;
- #6 durable chat + `ref_id`/citations foundation;
- #7 review queue foundation.

## 4. Рекомендуемый порядок

1. #7 Review Progress + hotkeys — быстрый фронт, высокая польза для демо.
2. #4 Snapshot Story — поверх готового diff.
3. #2 Why this node + #1 Provenance Score — один backend-агрегат + панель/бейдж.
4. #5 Red Flags — правила поверх дерева.
5. #8 Market Heat / #10 Auto-ZUN / #12 Cost — наши P2, если остаётся время.
6. #9 Coverage / #11 Run-to-run / #3 Tree Health — после MVP.

## 5. Проверки

- `go test ./...` — зелёный.
- `python scripts/check_deploy.py` — `deploy config OK`.
- `python scripts/demo_preflight.py` — `READY` (после рестарта backend проверить LLM env).
- `go run ./cmd/live-eval -env-file .secrets/llm.env` — 2/2 live.
