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
| [`notion_email_notifications.json`](notion_email_notifications.json) | Webhook `POST /webhook/notion-notify` + Manual | Map a generic Notion/page webhook payload (title, url, event, page id, actor, timestamp) and send it by email through SMTP; retry email delivery and log failures instead of crashing. |

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

### `notion_email_notifications.json`

Sends an email when a Notion page event arrives. The webhook is the reliable
integration path: no Notion credential/node is required, and the payload mapper
is generic, so any service that can POST JSON can feed the workflow instead of
Notion.

```
Notion automation / any service ──POST /webhook/notion-notify──┐
Manual test ────────────────────────────────────────────────────┴─▶ Normalize payload (Code)
                                                                          │
                                                                Config missing? (IF)
                                        missing env / bad payload ────────┴────── ready
                                              │                                     │
                                              ▼                                     ▼
                                   Log skipped notification        Send email via SMTP (retries x3, 5 s apart)
                                                                         │ success        │ error
                                                                         ▼                ▼
                                                                 Summary: email sent   Log email failure
                                                                         └───── both ─────▶ Done (no-op)
```

* **Webhook: notion-notify** — production URL
  `http://localhost:5678/webhook/notion-notify` (`POST`, JSON body). It answers
  immediately with `accepted` (`responseMode: onReceived`), so a slow SMTP
  server never blocks the caller.
* **Manual test** — runs the same flow without a payload and sends a demo
  notification, which is handy to verify the SMTP credential end to end.
* **Normalize payload** (Code) — maps generic field aliases:
  `title` (`page_title`, `name`, `subject`), `url` (`page_url`, `link`),
  `event` (`event_type`, `type`, `action`), `page_id` (`id`), `actor`
  (`edited_by`, `last_edited_by`, `user`), `timestamp` (`occurred_at`,
  `created_at`, `last_edited_time`). It also validates the SMTP/MAIL environment
  variables and records a `config_error`.
* **Config missing?** (IF) — true when `config_error` is non-empty (missing env
  vars, empty or non-JSON webhook body). The true branch logs the reason, the
  false branch sends the email, so a malformed event never crashes the workflow.
* **Send email via SMTP** — the n8n *Send Email* node. From/to are taken from
  `$env.MAIL_FROM` / `$env.MAIL_TO`; subject/body are n8n expressions containing
  `event`, `title`, `url`, `page_id`, `actor`, `timestamp` and the execution id.
  It retries twice (`retryOnFail`, `maxTries: 3`, 5 s apart) and
  `onError: continueErrorOutput` routes the final failure to *Log email failure*
  instead of aborting the workflow.
* **Summary: email sent / Log email failure / Log skipped notification** —
  normalize `status` (`sent` / `error` / `skipped`) plus context and the
  execution id for the **Executions** list; **Done (no-op)** joins all branches.
* **Settings** — execution history on, 1 h `executionTimeout`, timezone
  `Europe/Moscow`, imported **inactive**.

#### Payload contract

Any JSON POST to `/webhook/notion-notify` works; every field is optional and
falls back to a default:

```json
{
  "title": "Q3 competency review",
  "url": "https://www.notion.so/workspace/Q3-competency-review-abc123",
  "event": "page.updated",
  "page_id": "abc123",
  "actor": "ivan.petrov",
  "timestamp": "2026-11-18T12:00:00.000Z"
}
```

To adapt a different service (the informal "Нейтон" may not be Notion), edit the
`pick(...)` alias lists in the **Normalize payload** Code node — no other node
changes are needed. A Notion database trigger/poller is deliberately **not**
included: it would require a pre-configured Notion credential, while the webhook
needs only the URL.

#### Pointing Notion (or another service) at the webhook

1. Activate the workflow (toggle in the editor, or the commands in **Import**).
2. In the sending service, add a "send webhook"/HTTP POST action on the desired
   event (in Notion: a database automation such as *Page added/updated* with a
   *Send webhook* action) and map its variables to `title`, `url`, `event`,
   `page_id`, `actor`, `timestamp`:
   * same machine (curl, scripts, another container):
     `http://localhost:5678/webhook/notion-notify`;
   * Notion's cloud automation cannot reach `localhost` — expose n8n behind
     HTTPS (reverse proxy/tunnel) and use
     `https://<your-host>/webhook/notion-notify`.
3. Smoke-test with curl (the workflow must be **active** for the production URL):

```bash
curl -X POST http://localhost:5678/webhook/notion-notify \
  -H 'Content-Type: application/json' \
  -d '{"title":"Q3 competency review","url":"https://www.notion.so/page/abc123","event":"page.updated","page_id":"abc123","actor":"ivan.petrov","timestamp":"2026-11-18T12:00:00Z"}'
# -> accepted
```

While editing, click **Listen for test event** on the webhook node and POST to
`http://localhost:5678/webhook-test/notion-notify`; the production URL works
only when the workflow is active. The local webhook has **no authentication** by
default — before exposing it publicly set *Authentication → Header Auth* on the
webhook node (create an HTTP Header Auth credential) or restrict access at the
reverse proxy.

#### SMTP configuration

The workflow reads these variables from the `n8n` service environment (they must
also be passed through by `docker-compose.yml`; see the snippet below):

| Variable | Required | Meaning |
| -------- | -------- | ------- |
| `SMTP_HOST` | yes | SMTP server host, e.g. `smtp.yandex.ru` |
| `SMTP_PORT` | yes | SMTP port: `587` (STARTTLS) or `465` (SSL) |
| `SMTP_USER` | yes | SMTP login |
| `SMTP_PASS` | yes | SMTP password / app password |
| `MAIL_FROM` | yes | From address, e.g. `n8n <bot@example.com>` |
| `MAIL_TO` | yes | Notification recipient(s), comma-separated |

The **Normalize payload** node validates all six names; if one is missing it logs
the names to the *Log skipped notification* branch (visible under **Executions**)
instead of attempting a half-configured send. The actual SMTP transport, however,
comes from the n8n **SMTP account** credential: n8n credentials cannot read
`$env`, so the same host/port/user/password must be entered once in the
credential. `MAIL_FROM` / `MAIL_TO` are evaluated per execution from the
environment. Keep n8n's environment access enabled (the default — do not set
`N8N_BLOCK_ENV_ACCESS_IN_NODE=true`), otherwise `$env` expressions fail.

Create it in **n8n → Credentials → New → SMTP account**:

* Host / Port / User / Password = the `SMTP_*` values above;
* SSL/TLS: on for port 465, off for port 587 (STARTTLS is negotiated
  automatically); use *Ignore SSL Issues* only for self-signed relays;
* click **Test** (SMTP verify handshake), save, then open the workflow, select
  **Send email via SMTP** and choose the credential instead of the
  `REPLACE_WITH_SMTP_CREDENTIAL_ID` placeholder.

If the variables are not yet passed to the container, add them to the `n8n`
service in `docker-compose.yml` (or a `docker-compose.override.yml`) and to
`.env`:

```yaml
# docker-compose.yml -> services.n8n.environment
SMTP_HOST: "${SMTP_HOST:-}"
SMTP_PORT: "${SMTP_PORT:-587}"
SMTP_USER: "${SMTP_USER:-}"
SMTP_PASS: "${SMTP_PASS:-}"
MAIL_FROM: "${MAIL_FROM:-}"
MAIL_TO: "${MAIL_TO:-}"
```

Never commit real SMTP credentials: the n8n credential is encrypted in its
Postgres volume and `.env` is git-ignored.

## Import

```bash
cd .worktrees/platform
cp .env.example .env                       # set INGESTION_URL / TELEGRAM_CHAT_ID / SMTP_* / MAIL_* if needed
make up-automation                         # or: docker compose --profile automation up -d n8n n8n-postgres

# import the committed workflows
make import-n8n FILE=nightly_ingestion.json
make import-n8n FILE=notion_email_notifications.json
# equivalent:
docker compose --profile automation exec n8n n8n import:workflow --input=/workflows/nightly_ingestion.json

# activate after configuring credentials/environment
docker compose --profile automation exec n8n n8n update:workflow --id=<workflow-id> --active=true
docker compose --profile automation restart n8n
docker compose exec n8n n8n list:workflow     # verify
```

`make import-n8n` resolves `FILE` inside `/workflows` (the mounted directory),
so use just the file name. Importing does not activate the workflow.

> **Pinned n8n 1.63.4 CLI caveat (verified).** `n8n import:workflow
> --input=<file>` expects a JSON **array** of workflows, while the committed
> exports (and the n8n UI download/import) are single objects. If the command
> fails with `workflows.map is not a function`, import a directory of
> single-object files in `--separate` mode instead — this is the tested path and
> it also affects `nightly_ingestion.json`:
>
> ```bash
> # import every committed export in /workflows
> docker compose --profile automation exec n8n n8n import:workflow --separate --input=/workflows
> # or import exactly one file via a scratch directory (the mount is read-only)
> docker compose --profile automation exec n8n sh -c \
>   'mkdir -p /tmp/n8n-import && cp /workflows/notion_email_notifications.json /tmp/n8n-import/ && \
>    n8n import:workflow --separate --input=/tmp/n8n-import'
> ```
>
> Alternatively use **Workflow menu → Import from File** in the n8n UI, which
> accepts the single-object export directly.

## Validate exports

```bash
python3 deploy/n8n/workflows/validate_workflows.py
```

The script (standard library only) parses every `*.json` here and checks node /
connection integrity: required export keys, inactive flag, execution-history
settings, unique node names/ids, valid positions, IF and error branches, and that
every node is reachable from a trigger. It also prints the `$env.*` variables,
disabled nodes, retry settings and placeholder credentials per workflow — a quick
cross-check of the deployment wiring below. `make check-deploy`
(`scripts/check_deploy.py`) validates the same exports as part of CI.

## Required environment / credentials

Set these for the `n8n` service (wired in `docker-compose.yml` from `.env`;
`SMTP_*` / `MAIL_*` still need to be added to the service environment — see the
SMTP section above):

| Variable | Required | Meaning |
| -------- | -------- | ------- |
| `INGESTION_URL` | yes | Full URL of the ingestion endpoint, e.g. `http://api:8080/api/v1/ingest`. The workflow is a no-op without it. |
| `TELEGRAM_CHAT_ID` | only for alerts | Target chat for the disabled Telegram node, e.g. `-1001234567890`. |
| `N8N_BASIC_AUTH_*` | yes (local) | n8n UI login (from compose env). |
| `SMTP_HOST` / `SMTP_PORT` / `SMTP_USER` / `SMTP_PASS` | yes for email | SMTP relay used by the Notion notifications workflow. Must be passed to the `n8n` service environment (see the SMTP section above for the compose snippet). |
| `MAIL_FROM` / `MAIL_TO` | yes for email | From address and recipient(s) evaluated by the email node; validated together with the SMTP variables. |

Credentials (create in **n8n → Credentials**, never in git):

* `Telegram bot` — bot token from BotFather; assign it to the
  *Notify error* node and replace the placeholder credential id in the UI.
* `SMTP account` — host/port/user/password for the email workflow; assign it to
  the *Send email via SMTP* node and replace the placeholder credential id in
  the UI (details in the SMTP section above).
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
* The Notion notifications workflow answers the webhook before sending, retries
  email delivery twice (5 s apart) and records a final failure in the
  *Log email failure* branch; a skipped event (missing config/bad payload) is
  recorded by *Log skipped notification*. Neither branch aborts the execution.

## Exporting changes

* **Workflow menu → Download** (produces `*.json`), or
  `docker compose exec n8n n8n export:workflow --all --output=/workflows/all.json`,
  then commit the JSON here.
* Do not commit `.env`, credentials, tokens or chat ids. Keep `INGESTION_URL`,
  `TELEGRAM_CHAT_ID`, `SMTP_*` and `MAIL_*` in `.env` (git-ignored) or the
  deployment secret store.

See [`docs/N8N.md`](../../../docs/N8N.md) for the full runbook.
