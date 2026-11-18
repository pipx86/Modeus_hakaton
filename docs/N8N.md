# n8n automation (automation profile)

n8n runs on the `automation` profile so the default `docker compose up` stays
lightweight.

## Services

| Service | Image | Port | Persistence |
| ------- | ----- | ---- | ----------- |
| `n8n` | `n8nio/n8n:1.63.4` | 5678 | `n8n_data` volume (`/home/node/.n8n`) |
| `n8n-postgres` | `postgres:16-alpine` | internal | `n8n_pgdata` volume |

n8n uses **Postgres** (not SQLite) so workflows survive container restarts.
Both services have healthchecks and n8n waits for a healthy database.

## Start / stop

```bash
cd .worktrees/<branch>
cp .env.example .env                 # set N8N_DB_* / SMTP_* / MAIL_* if desired
docker compose --profile automation up -d n8n n8n-postgres
# or: make up-automation

docker compose logs -f n8n           # logs
make down                            # stop every profile (keeps volumes)
docker compose --profile automation down -v   # stop + delete automation data
```

Open <http://localhost:5678>. On a fresh database n8n shows the **owner setup**
screen — create the owner account there; access is then controlled by n8n's own
user management. The `N8N_BASIC_AUTH_*` variables in `docker-compose.yml` are
**ignored by n8n 1.63.4** (verified: `/rest/settings` answers `200`
unauthenticated and the image contains no basic-auth middleware) — do not rely
on them for access control.

## Configuration

| Variable | Default | Meaning |
| -------- | ------- | ------- |
| `N8N_PORT` | `5678` | host port |
| `N8N_HOST` | `localhost` | host name used in the editor/webhooks |
| `N8N_WEBHOOK_URL` | `http://localhost:5678/` | URL n8n advertises for webhooks |
| `N8N_BASIC_AUTH_*` | — | **ignored by n8n 1.63.4** (legacy compose vars); use UI owner setup |
| `N8N_DB_NAME` / `N8N_DB_USER` / `N8N_DB_PASSWORD` | `n8n` / `n8n` / `n8n_dev_password` | automation Postgres |
| `INGESTION_URL` | `http://api:8080/api/v1/ingest` | target of the nightly ingestion workflow |
| `TELEGRAM_CHAT_ID` | empty | target chat for the (disabled by default) error alert |
| `SMTP_HOST` / `SMTP_PORT` / `SMTP_USER` / `SMTP_PASS` | empty / `587` | SMTP relay for `notion_email_notifications.json` (pre-flight validation; the transport itself is an n8n *SMTP account* credential) |
| `MAIL_FROM` / `MAIL_TO` | empty | sender/recipient evaluated per execution by the email node |

`N8N_SECURE_COOKIE=false` is required for local plain HTTP access only.
`GENERIC_TIMEZONE` is left at the image default.

## Workflow lifecycle

n8n's CLI runs as the instance owner: on a brand-new database either create the
owner in the UI first or bootstrap it on a disposable environment with
`n8n user-management:reset` (verified to create an unclaimed owner; destructive).

n8n 1.63.4 does not accept a single-object workflow export through
`n8n import:workflow --input=<file>` (it expects a JSON array, error
`workflows.map is not a function`), so import the committed single-object
exports in `--separate` mode:

```bash
# import every committed export (or copy one file into a scratch dir first)
docker compose --profile automation exec n8n sh -c \
  'mkdir -p /tmp/n8n-import && cp /workflows/*.json /tmp/n8n-import/ && \
   n8n import:workflow --separate --input=/tmp/n8n-import'

# create the SMTP credential without the UI (plaintext JSON -> encrypted at rest)
docker compose --profile automation exec n8n n8n import:credentials --input=/tmp/smtp-credential.json

# list / activate workflows (activation requires an n8n restart)
docker compose --profile automation exec n8n n8n list:workflow
docker compose --profile automation exec n8n n8n update:workflow --id=<id> --active=true
docker compose --profile automation restart n8n

# export changes back into this repo
docker compose --profile automation exec n8n n8n export:workflow --all --output=/tmp/all.json
```

`make import-n8n FILE=...` still uses `--input=<single file>` and therefore hits
the pinned-image caveat above; use the `--separate` form. Full details,
including the `n8n import:credentials` JSON format, are in
[`deploy/n8n/workflows/README.md`](../deploy/n8n/workflows/README.md).

Exports live in [`deploy/n8n/workflows/`](../deploy/n8n/workflows/README.md)
and are mounted read-only at `/workflows`. n8n does not auto-import, so a fresh
stack starts clean.

## Committed workflow: `nightly_ingestion.json`

Daily **03:00** schedule plus a manual trigger. It POSTs to
`$env.INGESTION_URL` with `retryOnFail` and two retries (5 s apart), routes
failures through an IF node to a **Telegram alert node that is disabled by
default**, and normalizes success counts (`status`, `ingested`, `vacancies`,
`standards`, `finished_at`). Execution history is enabled; the workflow imports
inactive and the ingestion endpoint must be idempotent per source/day. Full
details: [`deploy/n8n/workflows/README.md`](../deploy/n8n/workflows/README.md).

The backend currently exposes no `/api/v1/ingest` HTTP route (`cmd/ingest` is a
CLI), so the compose default `INGESTION_URL` is a **placeholder for the future
ingestion endpoint**. Set it in `.env` to the real ingestion service/URL before
activating the workflow.

## Integration contract

n8n never talks to the LLM directly and never receives provider keys. It calls
the public API inside the compose network (`http://api:8080`):

* ingestion: `POST $INGESTION_URL` (nightly refresh of vacancies/standards)
* runs (planned/available): `POST /api/v1/runs`, `GET /api/v1/runs/{id}`
* operational: `GET /health`, `GET /metrics`

## Security notes

* Change all passwords in `.env`; `.env` is git-ignored and `.env.example`
  contains development placeholders only.
* The n8n encryption key lives in the `n8n_data` volume. For production set
  `N8N_ENCRYPTION_KEY` from a secret manager and back up the volume.
* `N8N_BASIC_AUTH_*` is **not supported by n8n 1.63.4**. A fresh instance has an
  unclaimed owner, so anyone who can reach port 5678 can claim it — do that
  immediately on first start, and never expose 5678 directly to the internet.
* The `notion-notify` webhook ships with `authentication: none`. Add Header Auth
  (or protect it at the reverse proxy) before exposing it publicly; see
  [`deploy/n8n/workflows/README.md`](../deploy/n8n/workflows/README.md).
* No credentials, tokens or chat ids are committed; Telegram credentials are
  created in the n8n UI and the error node ships disabled with a placeholder
  credential id. The SMTP credential is stored encrypted in Postgres; never
  commit credential exports (`n8n export:credentials --decrypted` is plaintext).

## Verification

### Static (CI-safe, no Docker daemon needed)

```bash
docker compose --profile automation config --quiet
python3 scripts/check_deploy.py
python3 deploy/n8n/workflows/validate_workflows.py
```

### End-to-end with a local SMTP sink (verified)

On 2026-11-18 the full email path was executed against `n8nio/n8n:1.63.4` +
`postgres:16-alpine` from the `automation` profile, with MailHog as the SMTP
sink. The committed workflow was imported **unchanged**; the SMTP credential was
created non-interactively with `n8n import:credentials` using the placeholder
credential id `REPLACE_WITH_SMTP_CREDENTIAL_ID` (disposable test only).

| Check | Result |
| ----- | ------ |
| `n8n import:credentials` (plaintext JSON) | `Successfully imported 1 credential`; Postgres value starts with `U2FsdGVk…` (encrypted, not plaintext) |
| workflow import (`--separate`) + activate + restart | imported unchanged; webhook registered |
| `POST /webhook/notion-notify` | HTTP 200, body `accepted` |
| MailHog `/api/v2/messages` | `total: 1`, expected From/To/Subject and normalized body |
| n8n execution | `status: success`, run data contains `Summary: email sent` and SMTP `250 Ok: queued as …` |
| empty body / SMTP sink down | skip branch and failure branch respectively; both executions still `success` |
| manual trigger (`n8n execute --id=…`) | demo email delivered |
| `n8n --version` | `1.63.4` |

Full copy-paste recipe (MailHog + credential import + webhook + sink check):
[`deploy/n8n/workflows/README.md`](../deploy/n8n/workflows/README.md#end-to-end-test-with-a-local-smtp-sink-verified).

**Not covered by the local test** (still needs a real environment):

* a real SMTP relay with TLS/STARTTLS, provider authentication and
  `SMTP_PORT=465/587` — MailHog is plaintext and accepts any AUTH;
* a **public HTTPS** webhook URL that Notion's cloud can reach (reverse
  proxy/tunnel + Header Auth), and the Notion automation itself;
* the real ingestion endpoint for `nightly_ingestion.json` (compose default is a
  placeholder — see above).

### Manual checklist for a real deployment

1. `cp .env.example .env`; fill `SMTP_*` / `MAIL_*` (and `INGESTION_URL`,
   `TELEGRAM_CHAT_ID` for the ingestion workflow). Keep `.env` out of git.
2. `docker compose --profile automation up -d n8n n8n-postgres`; open
   <http://localhost:5678> and create the owner account.
3. Create the **SMTP account** credential (UI → Credentials, then *Test*) or
   import it via `n8n import:credentials`; select it in **Send email via SMTP**
   instead of the `REPLACE_WITH_SMTP_CREDENTIAL_ID` placeholder.
4. Import the workflows with `--separate`, activate them, then
   `docker compose --profile automation restart n8n`.
5. Smoke-test the webhook with `curl` (expect `accepted`), confirm the email
   arrives, and check the *Executions* list (`sent` / `skipped` / `error`).
6. Only then expose the webhook over HTTPS with authentication and point the
   Notion automation at `https://<host>/webhook/notion-notify`.
