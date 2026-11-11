# n8n workflows

Workflow **exports** live in this directory and are mounted read-only into the
n8n container at `/workflows`:

```yaml
# docker-compose.yml, service n8n
volumes:
  - ./deploy/n8n/workflows:/workflows:ro
```

n8n does **not** auto-import from `/workflows`; import a committed workflow
explicitly (see below). Exports contain no credentials — n8n stores them
encrypted in its Postgres volume, never in JSON.

## Committed workflows

| File | Trigger | Purpose |
| ---- | ------- | ------- |
| [`nightly_ingestion.json`](nightly_ingestion.json) | Schedule `0 3 * * *` (daily 03:00) + Manual | Refresh vacancies/standards via the configurable ingestion endpoint, retry transient failures, alert on errors, summarize ingested counts. |

### `nightly_ingestion.json`

```
Every night 03:00 ─┐
Manual run ────────┴─▶ Ingest vacancies & standards (HTTP POST, 2 retries)
                         │ success                    │ request error
                         ▼                            ▼
                    Has error? ── true ──▶ Notify error (Telegram, disabled by default)
                         │ false
                         ▼
                    Summarize success (counts)
```

* **HTTP Request** — `POST {{ $env.INGESTION_URL }}` with
  `X-Run-ID: ingest-<date>-<time>` and JSON body
  `{"source":"n8n/nightly_ingestion","mode":"incremental","requested_at":…,"sources":["vacancies","standards"]}`.
  `retryOnFail: true`, `maxTries: 3` (2 retries), `waitBetweenTries: 5000` ms,
  120 s timeout, `onError: continueErrorOutput` so request failures take the
  error branch instead of stopping the workflow.
* **Has error?** (IF) — error when `error` is non-empty **or**
  `status != "ok"`. True branch → Telegram node; false branch → success summary.
* **Notify error (Telegram, disabled)** — sends the failure to
  `$env.TELEGRAM_CHAT_ID`. It is **disabled by default** and carries a
  placeholder credential id; enable it in the UI after creating a Telegram
  credential (see below).
* **Summarize success (counts)** — normalizes `status`, `ingested`, `vacancies`,
  `standards`, `finished_at` for reporting.
* **Settings** — execution history on (`saveExecutionProgress`,
  `saveManualExecutions`, `saveDataErrorExecution: all`,
  `saveDataSuccessExecution: all`), 1 h `executionTimeout`, timezone
  `Europe/Moscow`. Workflow is imported **inactive** (`"active": false`).

## Import

```bash
cd .worktrees/platform
cp .env.example .env                       # set INGESTION_URL / TELEGRAM_CHAT_ID if needed
make up-automation                         # or: docker compose --profile automation up -d n8n n8n-postgres

# import the committed workflow
make import-n8n FILE=nightly_ingestion.json
# equivalent:
docker compose --profile automation exec n8n n8n import:workflow --input=/workflows/nightly_ingestion.json

# activate after configuring credentials/environment
docker compose --profile automation exec n8n n8n update:workflow --id=<workflow-id> --active=true
docker compose --profile automation restart n8n
docker compose exec n8n n8n list:workflow     # verify
```

`make import-n8n` resolves `FILE` inside `/workflows` (the mounted directory),
so use just the file name. Importing does not activate the workflow.

## Required environment / credentials

Set these for the `n8n` service (already wired in `docker-compose.yml` from
`.env`):

| Variable | Required | Meaning |
| -------- | -------- | ------- |
| `INGESTION_URL` | yes | Full URL of the ingestion endpoint, e.g. `http://api:8080/api/v1/ingest`. The workflow is a no-op without it. |
| `TELEGRAM_CHAT_ID` | only for alerts | Target chat for the disabled Telegram node, e.g. `-1001234567890`. |
| `N8N_BASIC_AUTH_*` | yes (local) | n8n UI login (from compose env). |

Credentials (create in **n8n → Credentials**, never in git):

* `Telegram bot` — bot token from BotFather; assign it to the
  *Notify error* node and replace the placeholder credential id in the UI.
* Optionally header-auth/API-key credential for the ingestion endpoint; add it
  to the HTTP Request node if the API requires authentication.

## Idempotency expectations

* The workflow runs daily and can also be triggered manually at any time, so
  the ingestion endpoint **must be idempotent per source and day**: upsert
  vacancies/standards by their natural key and treat a re-run as a refresh, not
  an append. A repeated run must not create duplicates or double-count.
* `X-Run-ID` is derived from the trigger time (`ingest-yyyyMMdd-HHmm`); use it
  (or the execution id) for tracing/dedup at the API side.
* `mode: incremental` means "only changed/new records since the previous
  successful run". The API owns the watermark; n8n is stateless and retries
  may repeat a request.
* The success summary reports whatever counts the endpoint returns
  (`ingested`, `vacancies`, `standards`); missing fields fall back to `0`.

## Error handling and alerts

* Transient errors (timeouts, 5xx, connection resets) are retried twice with a
  5 s pause before the error branch runs.
* Request errors and business errors (`{"error": "…"}` or `{"status": "failed"}`)
  both reach the Telegram node when it is enabled.
* With the Telegram node disabled (repo default), failed runs still appear in
  **Executions** with the error payload — enable the node and its credential to
  get proactive alerts.

## Exporting changes

* **Workflow menu → Download** (produces `*.json`), or
  `docker compose exec n8n n8n export:workflow --all --output=/workflows/all.json`,
  then commit the JSON here.
* Do not commit `.env`, credentials, tokens or chat ids. Keep `INGESTION_URL`
  and `TELEGRAM_CHAT_ID` in `.env` (git-ignored) or the deployment secret
  store.

See [`docs/N8N.md`](../../../docs/N8N.md) for the full runbook.
