# Docker: running the full backend (`cmd/server`)

The default compose stack runs the **demo** `cmd/api` binary (in-memory store,
deterministic chat fallback, no database). This runbook runs the **real**
backend — `cmd/server` — in Docker against PostgreSQL, with goose migrations
applied before the backend starts.

If you only need the demo stack, see [DOCKER.md](DOCKER.md).

## What the `server` profile adds

| Compose service | Dockerfile target | Runs | Host port | Healthcheck |
| --------------- | ----------------- | ---- | --------- | ----------- |
| `migrate` | `migrate` | `goose up` over `./migrations`, one-shot | none | exit code (compose `service_completed_successfully`) |
| `server` | `server` | `cmd/server` (PostgreSQL + JWT + optional LLM agents) | `8082 -> 8080` | `/app/healthcheck` -> `GET /api/v1/health` |
| `postgres` | (image) | PostgreSQL 16 + pgvector | `127.0.0.1:5432` | `pg_isready` |

The existing demo services (`api`, `web`) are untouched and still start with a
plain `docker compose up`; the `server` profile is opt-in.

Ports: the demo api uses **8080**, the static web placeholder **8081**, so the
full backend gets **8082** by default. Everything is overridable via `.env`
(see [.env.example](../.env.example)).

## Prerequisites

* Docker Engine / Docker Desktop with Compose v2.24+ (the compose file uses
  `env_file: {path, required: false}` for optional secret files).
* No secrets are needed in git. The profile reads the git-ignored `.env` and
  `.secrets/llm.env` when they exist; without them `docker compose config`
  still passes (CI relies on that), but `cmd/server` will refuse to start
  without `JWT_SECRET`.

## Quick start

```bash
# From the repository root (this worktree: .worktrees/roman-ai).
cp .env.example .env          # optional; keep your local .env if you have one
# Put a real random JWT_SECRET (>= 32 bytes) into .env or .secrets/llm.env:
#   openssl rand -hex 32
# Optional live LLM agents: LLM_API_KEY / LLM_BASE_URL / LLM_MODEL in
# .secrets/llm.env (without them the backend still runs fallback interviews).

# Build the server + migration images and start the backend.
# Postgres (-v) and the one-shot migrate service start as dependencies.
docker compose --profile server up -d --build server

# Wait for the healthcheck, then probe the backend.
docker compose --profile server ps
curl -fsS http://127.0.0.1:8082/api/v1/health
curl -fsS -o /dev/null -w 'swagger: %{http_code}\n' http://127.0.0.1:8082/swagger/
curl -fsS http://127.0.0.1:8082/metrics | head
```

PowerShell equivalent:

```powershell
Copy-Item .env.example .env -ErrorAction SilentlyContinue
docker compose --profile server up -d --build server
curl.exe -fsS http://127.0.0.1:8082/api/v1/health
```

Important: always name the target service (`... up -d server`). Running
`docker compose --profile server up -d` **without** a service name also starts
the default demo api/web services, and the demo api wants host port 8080.

Stop the backend (Postgres keeps running; volumes are kept):

```bash
docker compose --profile server stop server
docker compose --profile server rm -f migrate server
```

## Build only (no containers)

The default image target is still the demo api, so the existing CI Docker smoke
job and `docker build .` are unchanged:

```bash
docker build -t activity-descriptor-api:local .                  # default target = api
docker build --target server  -t activity-descriptor-server:local .
docker build --target migrate -t activity-descriptor-migrate:local .
docker image inspect activity-descriptor-server:local  --format '{{.Size}} bytes'
docker image inspect activity-descriptor-migrate:local --format '{{.Size}} bytes'
```

`server` is a distroless image with the static `cmd/server` binary, the small
static `/app/healthcheck` probe, `api-docs/` (Swagger UI), `migrations/` and a
writable `logs/` directory. Agent prompts are Go-embedded at compile time, so
no prompt files are needed at runtime. Goose itself only ships in the `migrate`
image to keep the server image lean.

## Migrations

`docker compose --profile server up -d --build server` starts `migrate` first
(after Postgres is healthy). It applies every pending migration from
`/migrations` and exits:

* exit `0` -> compose starts `server`;
* non-zero -> compose fails the `up` and **does not start the backend**, so a
  stale schema is never served silently. The backend's own startup schema check
  (`SSE events` + `run_chat_messages` + `runs.stage/type` + `evidence.ref_id`)
  is a second safety net.

Manual operations:

```bash
# Apply (idempotent; already-applied versions are skipped)
docker compose --profile server run --rm migrate up

# Show the current migration state
docker compose --profile server run --rm migrate status

# Roll back the last migration (destructive; see migrations/README.md)
docker compose --profile server run --rm migrate down
```

Optional vector/embedding migrations (separate goose table, needs the pgvector
extension provided by the compose Postgres image) are not auto-applied:

```bash
docker compose --profile server run --rm migrate \
  -table goose_vector_versions -dir /migrations_optional/vector up
```

See [migrations/README.md](../migrations/README.md) for the migration workflow.

### Migrating an existing database

The `migrate` service points at the compose `postgres` service by default. To
run migrations and the backend against another PostgreSQL, set
`SERVER_DATABASE_URL` (it must be reachable **from the container**; a host
`localhost` is the container itself):

```bash
# PostgreSQL on the Docker host (Docker Desktop / Linux with host-gateway):
export SERVER_DATABASE_URL='postgres://activity:activity_dev_password@host.docker.internal:5432/activity_descriptor?sslmode=disable'
docker compose --profile server up -d --build server
```

The compose services map `host.docker.internal` to the host gateway, so this
also works on Linux. For the local Postgres container that the native backend
uses, the same DSN via `host.docker.internal:5432` applies as long as that
container is running.

## Environment and secrets

Precedence for the `server` container: compose `environment` (explicit) >
`env_file` entries (later file wins) > image defaults.

| Source | Where | What |
| ------ | ----- | ---- |
| `.env` (git-ignored) | auto-loaded by compose for interpolation **and** passed via `env_file` | `HH_*` credentials, optional `JWT_SECRET` / `LLM_*`, ports (`SERVER_PORT`, `SERVER_DATABASE_URL`, `POSTGRES_*`) |
| `.secrets/llm.env` (git-ignored) | `env_file` | `JWT_SECRET`, `LLM_API_KEY`, `LLM_BASE_URL`, `LLM_MODEL`, per-agent overrides |
| Shell environment | compose interpolation | `SERVER_PORT`, `SERVER_DATABASE_URL`, `APP_ENV`, `LOG_LEVEL`, `POSTGRES_*` |

`.env` and `.secrets/llm.env` are optional in the compose file
(`required: false`), so `docker compose config` passes in CI where neither file
is checked out; `.env.example` documents every variable.

Required for the full backend:

* `JWT_SECRET` — HMAC secret for bearer tokens, >= 32 bytes. Generate with
  `openssl rand -hex 32`. There is intentionally **no** default placeholder for
  the `server` profile: a production-shaped backend should not start on a
  known secret.
* `DATABASE_URL` — set automatically to the compose `postgres` service; override
  with `SERVER_DATABASE_URL` only when needed.

Optional:

* `LLM_*` — without a provider key the server logs
  `LLM provider is not configured; using fallback interview` and runs the
  deterministic fallback; it never crashes. `LLM_API_KEY_FILE` paths are inside
  the container, so prefer putting the key itself into `.secrets/llm.env`.
* `OTEL_EXPORTER_OTLP_ENDPOINT` — empty disables trace export; set
  `http://jaeger:4318` with the monitoring profile for the local collector.
* `HH_*`, `OLLAMA_*`, `CORS_ALLOWED_ORIGINS`, `SSE_HEARTBEAT_S`, `LOG_LEVEL`,
  `LOG_FILE` — passed through the same env files when needed.

Nothing is baked into the image; `.dockerignore` keeps `.env`, `.secrets/` and
other local data out of the build context.

## Health checks

```bash
# Compose health status (healthy once /api/v1/health returns 2xx)
docker compose --profile server ps
docker inspect --format '{{.Name}} {{.State.Health.Status}}' activity-descriptor-server-1

# Manual probes
curl -fsS http://127.0.0.1:8082/api/v1/health
curl -fsS -o /dev/null -w 'swagger: %{http_code}\n' http://127.0.0.1:8082/swagger/
curl -fsS -o /dev/null -w 'swagger spec: %{http_code}\n' http://127.0.0.1:8082/swagger/swagger.json

# Same probe the container runs (distroless: no shell/curl)
docker exec activity-descriptor-server-1 /app/healthcheck
```

Container logs go to stdout (`docker compose --profile server logs -f server`)
and to the in-container rotating file `/app/logs/app.log` (`LOG_FILE`
overrides it). The Swagger UI is served from `./api-docs` relative to the
container working directory `/app`.

## JWT-protected requests

Tokens must be signed with the same `JWT_SECRET` the server uses. The demo api
image already ships `/local-token`; use it as a small token issuer:

```bash
# Read the secret from your local env file without printing it.
JWT_SECRET="$(grep -E '^JWT_SECRET=' .secrets/llm.env | cut -d= -f2-)"   # or export it yourself
TOKEN="$(docker run --rm -e JWT_SECRET="$JWT_SECRET" \
  --entrypoint /local-token activity-descriptor-api:local -role admin)"

curl -fsS -X POST http://127.0.0.1:8082/api/v1/runs \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"profession":"data analyst"}'

curl -fsS http://127.0.0.1:8082/api/v1/runs/<run-id> -H "Authorization: Bearer $TOKEN"
```

Without a valid token the protected routes return 401. With `LLM_*` configured,
creating a run can make real provider calls (paid); without them the fallback
pipeline is used.

## Switching between demo api and full server

| Goal | Command | Host URL |
| ---- | ------- | -------- |
| Demo api + web + postgres | `docker compose up -d --build api web postgres` | http://localhost:8080 (api), http://localhost:8081 (web) |
| Full backend + postgres + migrations | `docker compose --profile server up -d --build server` | http://localhost:8082 |
| Full backend only (it must already be built) | `docker compose --profile server up -d server` | http://localhost:8082 |
| Stop the full backend | `docker compose --profile server stop server` | — |
| Stop/remove the full backend services | `docker compose --profile server rm -f migrate server` | — |
| Stop the whole project (keeps volumes) | `docker compose --profile server --profile monitoring --profile automation down` | — |

Both stacks can run at the same time: they use different host ports and the
demo api keeps its in-memory store, while `server` uses the shared compose
PostgreSQL volume. Migrations only touch that PostgreSQL, never the demo api.

Makefile shortcuts: `make up-server` builds and starts the full backend,
`make down-server` stops its services and `make check-compose` validates the
default and all profiles (including `server`).

## Verification evidence (this worktree, 2026-11-19)

Executed with Docker 29.9 / Compose v5.6.1 reachable. Every runtime check used
an **isolated compose project and host ports** (`-p cga-*`, `SERVER_PORT`
18090-18098, `POSTGRES_PORT` 55433-55436), so the demo stack and the host
backend on :8080 were never touched. All test containers, networks, volumes and
compose projects were removed afterwards; the host backend was re-checked
(`/api/v1/health` 200) and the pre-existing monitoring stack was left running.

| Check | Command | Result |
| ----- | ------- | ------ |
| deploy assets | `python scripts/check_deploy.py` | `deploy config OK` (Dockerfile, compose incl. `server` profile, docs, secrets scan) |
| compose config | `docker compose config --quiet`; `--profile server`; `--profile monitoring --profile automation` | all exit 0 |
| compose config without secrets (CI) | same commands with `.env` and `.secrets/llm.env` temporarily absent | all exit 0; rendered config contains no key material; both files restored byte-identical (SHA-256) |
| default api image (CI target) | `docker build -t activity-descriptor-api:local .` | built, **27,002,583 B** (< 100 MB CI budget) |
| server image | `docker build --target server ...` | built, **~42.8 MB** (42,750,170 B) |
| migrate image | `docker build --target migrate ...` | built, **~22.9 MB** (22,902,055 B), goose v3.29.0 (Postgres driver only) |
| migration from empty DB | migrate image `up` against empty pgvector Postgres | 17 migrations applied, goose version `20261118030000`, 28 public tables |
| migration re-run / status | migrate image `status` | already applied, no-op (idempotent) |
| optional vector group | migrate image `-table goose_vector_versions -dir /migrations_optional/vector up` | applied successfully |
| failing migration | migrate image with bad credentials | exit code 1 |
| server startup | server image against migrated DB (no LLM keys) | healthy in ~6 s, startup schema check passed, fallback interview logged |
| health + Swagger | GET `/api/v1/health`, `/swagger/`, `/swagger/swagger.json` | 200 / 200 / 200 (199,009 B spec) |
| metrics | GET `/metrics` | 200, 32 `activity_descriptor_*` lines |
| auth | POST `/api/v1/runs` without a token | 401 |
| JWT flow | token via the api image `/local-token`, then POST/GET/messages | 201 / 200 / 2 fallback agent messages |
| in-container probe | `/app/healthcheck` | exit 0 |
| compose profile e2e | `docker compose -p <iso> --profile server up -d --build server` | postgres healthy -> migrate exit 0 -> server healthy; `/api/v1/health` 200, `/swagger/` 200; migrate container `Exited (0)` |
| cold start | images removed, same compose command | one command builds **both** `server` and `migrate` targets, then starts the backend healthy |
| failure gate | compose `up` with an unreachable `SERVER_DATABASE_URL` | `up` exit 1, migrate `Exited (1)`, server container `Created` but **never started** |
| demo image smoke | api image container (CI-equivalent) | `/health` 200 `mode=demo`, `/metrics` 200, `/api/v1/health` 200, self-healthcheck 0, JWT run 201 + agent messages |

Note: `docker compose config` renders the contents of the local `env_file`
entries when they exist, so its output can contain secrets locally — do not
paste it into reports. In CI neither file is checked out, so the rendered
config is secret-free (verified above).

## Troubleshooting

| Symptom | Cause / fix |
| ------- | ----------- |
| `server` exits with `DATABASE_URL and JWT_SECRET are required` | Add `JWT_SECRET` (and optionally `DATABASE_URL`) to `.env` or `.secrets/llm.env`. Both env files are resolved relative to the repository root. |
| `server` exits with `база не подготовлена: примените основные миграции...` | Migrations are missing/older than the code: `docker compose --profile server run --rm migrate up`, then restart `server`. |
| `migrate` exits non-zero | Check `docker compose --profile server logs migrate`; fix the DSN/DB and re-run. The backend correctly refuses to start in this case. |
| `server` unhealthy / no HTTP | `docker compose --profile server logs server`; the startup order is Postgres healthy -> migrate exit 0 -> server. |
| `bind: address already in use` on start | Host port 8082 is taken: set `SERVER_PORT` (e.g. `8083`) in `.env`. |
| Protected route returns 401 | Token was issued with a different `JWT_SECRET`; re-issue with the same secret the compose profile uses. |
| `invalid PostgreSQL configuration` / `cannot connect to PostgreSQL` | `SERVER_DATABASE_URL` points at an unreachable host, or at a host `localhost` from inside the container. Use the compose service name or `host.docker.internal`. |
| `docker compose config` fails in CI | Should not happen: the optional env files use `required: false`. Run `docker compose config --quiet` locally to reproduce. |
