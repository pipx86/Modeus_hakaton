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
cd .worktrees/devops
cp .env.example .env                 # set N8N_BASIC_AUTH_* / N8N_DB_* if desired
docker compose --profile automation up -d n8n n8n-postgres
# or: make up-automation

docker compose logs -f n8n           # logs
make down                            # stop every profile (keeps volumes)
docker compose --profile automation down -v   # stop + delete automation data
```

Open <http://localhost:5678>; log in with `N8N_BASIC_AUTH_USER` /
`N8N_BASIC_AUTH_PASSWORD` (defaults `admin` / `n8n_dev_password` from
`.env.example`).

## Configuration

| Variable | Default | Meaning |
| -------- | ------- | ------- |
| `N8N_PORT` | `5678` | host port |
| `N8N_HOST` | `localhost` | host name used in the editor/webhooks |
| `N8N_WEBHOOK_URL` | `http://localhost:5678/` | URL n8n advertises for webhooks |
| `N8N_BASIC_AUTH_ACTIVE` | `true` | username/password gate |
| `N8N_BASIC_AUTH_USER` | `admin` | basic auth user |
| `N8N_BASIC_AUTH_PASSWORD` | `n8n_dev_password` | basic auth password (override!) |
| `N8N_DB_NAME` / `N8N_DB_USER` / `N8N_DB_PASSWORD` | `n8n` / `n8n` / `n8n_dev_password` | automation Postgres |
| `INGESTION_URL` | `http://api:8080/api/v1/ingest` | target of the nightly ingestion workflow |
| `TELEGRAM_CHAT_ID` | empty | target chat for the (disabled by default) error alert |

`N8N_SECURE_COOKIE=false` is required for local plain HTTP access only.
`GENERIC_TIMEZONE` is left at the image default.

## Workflow lifecycle

```bash
# import the committed nightly ingestion workflow (resolved inside /workflows)
make import-n8n FILE=nightly_ingestion.json
# equivalent:
docker compose --profile automation exec n8n n8n import:workflow --input=/workflows/nightly_ingestion.json

# export changes back into this repo
docker compose exec n8n n8n export:workflow --all --output=/workflows/all.json

# list / activate workflows
docker compose exec n8n n8n list:workflow
docker compose exec n8n n8n update:workflow --id=<id> --active=true
```

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
* Exposing port 5678 publicly requires TLS and `N8N_SECURE_COOKIE=true`; the
  local compose file is not internet-hardened.
* No credentials, tokens or chat ids are committed; Telegram credentials are
  created in the n8n UI and the error node ships disabled with a placeholder
  credential id.

## Verification

Docker is not reachable from the authoring session, so this stack was **not
started**; it was validated statically (`docker compose --profile automation
config --quiet` exits 0, `scripts/check_deploy.py` validates the workflow JSON,
connections, retry/disabled flags and env wiring). To verify later:

```bash
docker compose --profile automation up -d n8n n8n-postgres
docker compose ps n8n n8n-postgres
curl -fsS -u admin:n8n_dev_password http://localhost:5678/healthz
docker compose exec n8n n8n --version
make import-n8n FILE=nightly_ingestion.json
```
