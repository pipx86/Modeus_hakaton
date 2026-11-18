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

The workflow reads these variables from the `n8n` service environment;
`docker-compose.yml` already passes all of them through from `.env`:

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
comes from the n8n **SMTP account** credential. This is a hard requirement of the
node, not a configuration choice: in n8n 1.63.4 the *Send Email*
(`n8n-nodes-base.emailSend`) node declares
`credentials: [{name: "smtp", required: true}]` and builds its transport only from
the credential fields. **The email node cannot read SMTP settings from
environment variables** — `SMTP_*` can never replace the credential. `$env` is
used only for `MAIL_FROM` / `MAIL_TO` in the node's `fromEmail` / `toEmail`
expressions and for the pre-flight validation in the Code node. Keep n8n's
environment access enabled (the default — do not set
`N8N_BLOCK_ENV_ACCESS_IN_NODE=true`), otherwise `$env` expressions fail.

Create the credential in one of two ways:

**Option A — n8n UI (normal deployments):** **n8n → Credentials → New → SMTP
account**:

* Host / Port / User / Password = the `SMTP_*` values above;
* SSL/TLS: on for port 465, off for port 587 (STARTTLS is negotiated
  automatically); use *Ignore SSL Issues* only for self-signed relays;
* click **Test** (SMTP verify handshake), save, then open the workflow, select
  **Send email via SMTP** and choose the credential instead of the
  `REPLACE_WITH_SMTP_CREDENTIAL_ID` placeholder.

**Option B — non-interactive CLI import (verified):** `n8n import:credentials`
accepts a JSON **array** of credentials whose `data` is **plaintext**; it
encrypts the data with the instance encryption key before storing it (the
Postgres value starts with `U2FsdGVk…`, not with the plaintext). `id` is an
optional string primary key — a UUID/nanoid is generated when it is omitted. The
CLI acts as the instance owner, so an owner must exist first: on a **fresh**
database run `n8n user-management:reset` once (creates an unclaimed owner;
destructive — disposable environments only), or complete the owner setup in the
UI on first start.

```json
[
  {
    "name": "SMTP account",
    "type": "smtp",
    "data": {
      "user": "<smtp-user>",
      "password": "<smtp-password>",
      "host": "smtp.example.com",
      "port": 587,
      "secure": false,
      "disableStartTls": false
    }
  }
]
```

```bash
docker compose --profile automation exec n8n n8n import:credentials --input=/tmp/smtp-credential.json
# -> Successfully imported 1 credential.
```

Never commit that JSON (or an `n8n export:credentials --decrypted` output) — both
contain secrets in plaintext. The complete throwaway local recipe, which imports
the committed workflow **unchanged**, is in **End-to-end test** below.

`docker-compose.yml` **already** passes these variables to the `n8n` service
(verified in the running container); set the values in `.env`:

```yaml
# docker-compose.yml -> services.n8n.environment (already present)
SMTP_HOST: "${SMTP_HOST:-}"
SMTP_PORT: "${SMTP_PORT:-587}"
SMTP_USER: "${SMTP_USER:-}"
SMTP_PASS: "${SMTP_PASS:-}"
MAIL_FROM: "${MAIL_FROM:-}"
MAIL_TO: "${MAIL_TO:-}"
```

An empty `SMTP_*` / `MAIL_*` set makes the workflow take the *Log skipped
notification* branch (by design) instead of failing. Never commit real SMTP
credentials: the n8n credential is encrypted in its Postgres volume and `.env`
is git-ignored.

## End-to-end test with a local SMTP sink (verified)

The whole path — production webhook → Code node → SMTP credential → SMTP sink —
was **verified locally** against the pinned `n8nio/n8n:1.63.4` image with
[MailHog](https://github.com/mailhog/MailHog) as the sink, including the skip and
failure branches and the manual-trigger demo. No real credentials are involved;
every value below is a throwaway test value. The committed
[`notion_email_notifications.json`](notion_email_notifications.json) is imported
**unchanged**: the imported credential deliberately uses the placeholder id
`REPLACE_WITH_SMTP_CREDENTIAL_ID`, which is exactly the id the export references,
so the E2E test also proves the export's credential wiring.

Prerequisites: Docker with the `automation` profile usable and a **fresh** n8n
database. Step 3 resets the instance owner and step 4 imports a plaintext
credential — both are destructive and for disposable environments only.

```bash
# 0) clean slate (drops the automation volumes)
docker compose --profile automation down -v

# 1) SMTP sink: SMTP on 1025, web UI/API on 8025
docker run -d --name n8n-mailhog -p 1025:1025 -p 8025:8025 mailhog/mailhog:v1.0.1

# 2) start n8n with throwaway SMTP/MAIL settings
#    (bash; on PowerShell use $env:SMTP_HOST="..." etc., and write files as UTF-8 without BOM)
export SMTP_HOST=host.docker.internal   # Docker Desktop; on Linux add --add-host=host.docker.internal:host-gateway
export SMTP_PORT=1025
export SMTP_USER=mailhog                # MailHog accepts any AUTH PLAIN credentials
export SMTP_PASS=mailhog
export MAIL_FROM="n8n <n8n@example.test>"
export MAIL_TO="notifications@example.test"
docker compose --profile automation up -d n8n n8n-postgres
until curl -fsS http://localhost:5678/healthz; do sleep 2; done

# 3) bootstrap the instance owner on the fresh database (needed by the CLI imports)
docker compose --profile automation exec n8n n8n user-management:reset
# -> Successfully reset the database to default user state.

# 4) create the SMTP credential non-interactively (plaintext in, encrypted at rest)
cat > /tmp/smtp-credential.json <<'JSON'
[
  {
    "id": "REPLACE_WITH_SMTP_CREDENTIAL_ID",
    "name": "SMTP account",
    "type": "smtp",
    "data": {
      "user": "mailhog",
      "password": "mailhog",
      "host": "host.docker.internal",
      "port": 1025,
      "secure": false,
      "disableStartTls": false
    }
  }
]
JSON
CID=$(docker compose --profile automation ps -q n8n)
docker cp /tmp/smtp-credential.json "$CID":/tmp/smtp-credential.json
docker compose --profile automation exec n8n n8n import:credentials --input=/tmp/smtp-credential.json
# -> Successfully imported 1 credential.

# 5) import the committed export unchanged, activate it, restart n8n
docker compose --profile automation exec n8n sh -c \
  'mkdir -p /tmp/n8n-import && cp /workflows/notion_email_notifications.json /tmp/n8n-import/ && \
   n8n import:workflow --separate --input=/tmp/n8n-import'
docker compose --profile automation exec n8n n8n list:workflow   # copy the workflow id
docker compose --profile automation exec n8n n8n update:workflow --id=<workflow-id> --active=true
docker compose --profile automation restart n8n

# 6) trigger the production webhook
curl -X POST http://localhost:5678/webhook/notion-notify \
  -H 'Content-Type: application/json' \
  -d '{"title":"Q3 competency review","url":"https://www.notion.so/page/abc123","event":"page.updated","page_id":"abc123","actor":"ivan.petrov","timestamp":"2026-11-18T12:00:00Z"}'
# -> accepted

# 7) check the sink
curl -s http://localhost:8025/api/v2/messages
# -> {"total":1,...}: Subject "[Notion] page.updated: Q3 competency review",
#    From n8n@example.test, To notifications@example.test, body with URL/actor/timestamp

# cleanup
docker rm -f n8n-mailhog
docker compose --profile automation down -v
```

Verified results (2026-11-18, `n8nio/n8n:1.63.4`):

| Check | Observed result |
| ----- | --------------- |
| `n8n import:credentials` with plaintext JSON | `Successfully imported 1 credential.`; Postgres stores `U2FsdGVk…` (encrypted with the instance key), not plaintext |
| `n8n import:workflow --separate` | `Successfully imported 1 workflow.`; placeholder credential id preserved in the DB and resolved at execution |
| webhook trigger | HTTP `200`, body `accepted` |
| MailHog API `/api/v2/messages` | `total: 1`; From `n8n@example.test`, To `notifications@example.test`, Subject `[Notion] page.updated: Q3 competency review`; text body contains URL, page id, actor, timestamp and the n8n execution id |
| n8n execution | `status: success`; run data contains `Summary: email sent` and SMTP response `250 Ok: queued as …`; neither error nor skip node ran |
| empty body `{}` | HTTP `200 accepted`; execution success through *Log skipped notification*; no email |
| SMTP sink stopped | HTTP `200 accepted`; 3 attempts, then *Log email failure* with `ECONNREFUSED`; execution still success |
| `n8n execute --id=<id>` (manual trigger) | execution success; demo email `[Notion] workflow.test: Manual test of the Notion notification workflow` delivered |
| `n8n --version` | `1.63.4` |

Notes and limits:

* The credential `id` must match the id referenced by the node.
  `REPLACE_WITH_SMTP_CREDENTIAL_ID` is used only to test the committed export
  **unmodified**; for a real deployment create the credential normally and
  either select it in the UI or import a copy of the workflow with the real id
  substituted. Never commit credential exports (`--decrypted` writes plaintext
  secrets).
* The webhook was reached at `http://localhost:5678` (same machine). A real
  Notion cloud automation needs a **public HTTPS** URL — see the exposure notes
  above; a real SMTP relay (TLS/STARTTLS, provider auth, port 465/587) is also
  **not** covered by the MailHog test, which is plaintext and unauthenticated by
  design.
* On a fresh stack the editor has no login until an owner is created; the
  `N8N_BASIC_AUTH_*` variables in compose are **ignored by n8n 1.63.4**
  (verified: `/rest/settings` answers `200` unauthenticated and the image
  contains no basic-auth middleware). This affects only UI access, not the
  import/webhook flow.

## Import

```bash
cd .worktrees/<branch>
cp .env.example .env                       # set INGESTION_URL / TELEGRAM_CHAT_ID / SMTP_* / MAIL_* if needed
docker compose --profile automation up -d n8n n8n-postgres   # or: make up-automation

# first start: create the owner account in the UI (http://localhost:5678), or
# on a disposable database bootstrap it non-interactively:
#   docker compose --profile automation exec n8n n8n user-management:reset
# The n8n CLI runs as the instance owner; without an owner the imports below
# fail with "Failed to find owner" on a brand-new database.

# import the committed workflows (single-object exports => --separate, see caveat)
docker compose --profile automation exec n8n sh -c \
  'mkdir -p /tmp/n8n-import && cp /workflows/*.json /tmp/n8n-import/ && \
   n8n import:workflow --separate --input=/tmp/n8n-import'

# import the SMTP credential without the UI (plaintext JSON, encrypted at rest;
# see "SMTP configuration" for the file format and how to get it into the container)
docker compose --profile automation exec n8n n8n import:credentials --input=/tmp/smtp-credential.json

# activate after configuring credentials/environment
docker compose --profile automation exec n8n n8n list:workflow     # verify, copy the id
docker compose --profile automation exec n8n n8n update:workflow --id=<workflow-id> --active=true
docker compose --profile automation restart n8n
```

`--separate` imports every `*.json` in the directory. The `make import-n8n`
target resolves `FILE` inside `/workflows` (the mounted directory), but it uses
`--input=<single file>` and therefore hits the pinned-image caveat below; use the
`--separate` commands above on n8n 1.63.4. Importing does not activate the
workflow, and activation only takes effect after an n8n restart (the CLI prints
the same warning).

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

Set these for the `n8n` service (`docker-compose.yml` already passes
`INGESTION_URL`, `TELEGRAM_CHAT_ID`, `SMTP_*` and `MAIL_*` through from `.env` —
leave the `.env` values empty to skip the corresponding workflow):

| Variable | Required | Meaning |
| -------- | -------- | ------- |
| `INGESTION_URL` | yes | Full URL of the ingestion endpoint, e.g. `http://api:8080/api/v1/ingest`. The workflow is a no-op without it. |
| `TELEGRAM_CHAT_ID` | only for alerts | Target chat for the disabled Telegram node, e.g. `-1001234567890`. |
| `N8N_BASIC_AUTH_*` | ignored | Still passed by compose, but **n8n 1.63.4 ignores basic-auth environment variables** (verified: `/rest/settings` answers `200` unauthenticated and the image has no basic-auth middleware). Access is controlled by n8n user management — create the owner account in the UI on first start. |
| `SMTP_HOST` / `SMTP_PORT` / `SMTP_USER` / `SMTP_PASS` | yes for email | SMTP relay used by the Notion notifications workflow; wired into the `n8n` service environment by compose. |
| `MAIL_FROM` / `MAIL_TO` | yes for email | From address and recipient(s) evaluated by the email node; validated together with the SMTP variables. |

Credentials (create in **n8n → Credentials** or import non-interactively with
`n8n import:credentials`, never in git):

* `Telegram bot` — bot token from BotFather; assign it to the
  *Notify error* node and replace the placeholder credential id in the UI.
* `SMTP account` — host/port/user/password for the email workflow; assign it to
  the *Send email via SMTP* node and replace the placeholder credential id in
  the UI, or import it via the CLI (details in the SMTP section above).
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
