# Docker: running the full backend (`cmd/server`) + web UI

The default compose stack runs the **demo** `cmd/api` binary (in-memory store,
deterministic chat fallback, no database). This runbook runs the **real**
backend — `cmd/server` — in Docker against PostgreSQL, with both goose migration
groups applied before the backend starts, plus the built React frontend served
by nginx as a single public entry point.

If you only need the demo stack, see [DOCKER.md](DOCKER.md).

## What the `server` profile adds

| Compose service | Dockerfile target | Runs | Host port | Healthcheck |
| --------------- | ----------------- | ---- | --------- | ----------- |
| `migrate` | `migrate` | `goose up` over `./migrations` (main group), one-shot | none | exit code (compose `service_completed_successfully`) |
| `migrate-vector` | `migrate` | `goose -table goose_vector_versions -dir /migrations_optional/vector up` (optional vector group), one-shot | none | exit code (compose `service_completed_successfully`) |
| `server` | `server` | `cmd/server` (PostgreSQL + JWT + optional LLM agents) | `8082 -> 8080` (direct access, `SERVER_PORT`) | `/app/healthcheck` -> `GET /api/v1/health` |
| `web-server` | `web` | nginx + built React SPA (`frontend/dist`) + reverse proxy to `server` | `8083 -> 80` (`SERVER_WEB_PORT`) | `GET /healthz` (nginx itself) |
| `postgres` | (image) | PostgreSQL 16 + pgvector | `127.0.0.1:5432` | `pg_isready` |

The existing demo services (`api`, `web`) are untouched and still start with a
plain `docker compose up`; the `server` profile is opt-in.

**One public entry point.** The browser opens **<http://localhost:8083>** only:
`web-server` serves the SPA and proxies the API, Swagger UI and metrics through
the same origin, so there is no second port to open and no CORS configuration:

```text
browser :8083 --> web-server (nginx)
                    |-- /            -> frontend/dist (SPA, try_files fallback)
                    |-- /api/        -> http://server:8080/api/...      (SSE not buffered)
                    |-- /swagger/    -> http://server:8080/swagger/...  (Swagger UI + spec)
                    `-- /metrics     -> http://server:8080/metrics      (Prometheus text)
```

`server:8082` stays published for direct backend debugging and for Prometheus
scrape parity (inside the compose network Prometheus scrapes `server:8080`
directly). Ports are overridable via the shell or `.env`: `SERVER_PORT`
(default 8082) and `SERVER_WEB_PORT` (default 8083, the single public web port).

## Prerequisites

* Docker Engine / Docker Desktop with Compose v2.24+ (the compose file uses
  `env_file: {path, required: false}` for optional secret files). The `web`
  image build also needs the `node:22.20-bookworm-slim` base image; `npm ci`
  runs inside the Docker build (the host needs no Node.js).
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

# Build the backend, migration and web images and start the full stack.
# Postgres (-v), migrate and migrate-vector start as dependencies.
docker compose --profile server up -d --build server web-server

# Wait for the healthchecks, then probe the single public entry point.
docker compose --profile server ps
curl -fsS -o /dev/null -w 'spa:     %{http_code}\n' http://127.0.0.1:8083/
curl -fsS -o /dev/null -w 'health:  %{http_code}\n' http://127.0.0.1:8083/api/v1/health
curl -fsS -o /dev/null -w 'swagger: %{http_code}\n' http://127.0.0.1:8083/swagger/
curl -fsS http://127.0.0.1:8083/metrics | head

# Optional: direct backend port (same backend, bypasses nginx).
curl -fsS http://127.0.0.1:8082/api/v1/health
```

Open <http://localhost:8083> in a browser for the frontend.

PowerShell equivalent:

```powershell
Copy-Item .env.example .env -ErrorAction SilentlyContinue
docker compose --profile server up -d --build server web-server
curl.exe -fsS http://127.0.0.1:8083/api/v1/health
```

Important: always name the target services (`... up -d server web-server`).
Running `docker compose --profile server up -d` **without** service names also
starts the default demo api/web services, and the demo api wants host port 8080.

Stop the stack (Postgres keeps running; volumes are kept):

```bash
docker compose --profile server stop server web-server
docker compose --profile server rm -f migrate migrate-vector server web-server
```

`make up-server` wraps the same build+start command, `make down-server` removes
the services and `make up-server-monitoring` also starts Prometheus/Grafana with
the server scrape config.

## The web image (frontend build + nginx)

`Dockerfile` builds the frontend inside the image (`frontend-build` stage):

1. `node:22.20-bookworm-slim` runs `npm ci` (`frontend/package-lock.json`) and
   `npm run build` (`tsc -b && vite build`), producing `frontend/dist`;
2. the `web` stage copies that `dist/` into `nginx:1.27-alpine` and bakes in
   [`deploy/nginx/server.conf`](../deploy/nginx/server.conf).

Nothing from the Node toolchain ships in the final image (nginx + static files
only). The SPA uses relative `/api/v1` and `/metrics` URLs (no `VITE_API_URL`),
which is why the same build works on port 8083, 80 or behind another proxy.

Build it alone:

```bash
docker build --target frontend-build -t activity-descriptor-frontend:local .
docker build --target web -t activity-descriptor-web:local .
docker image inspect activity-descriptor-web:local --format '{{.Size}} bytes'
```

If the frontend build fails, reproduce it exactly with `cd frontend && npm ci &&
npm run build` (that is also what the CI `frontend` job runs).

## Build only (no containers)

The default image target is still the demo api, so the existing CI Docker smoke
job and `docker build .` are unchanged:

```bash
docker build -t activity-descriptor-api:local .                  # default target = api
docker build --target server  -t activity-descriptor-server:local .
docker build --target migrate -t activity-descriptor-migrate:local .
docker build --target web     -t activity-descriptor-web:local .
docker image inspect activity-descriptor-server:local  --format '{{.Size}} bytes'
docker image inspect activity-descriptor-migrate:local --format '{{.Size}} bytes'
docker image inspect activity-descriptor-web:local     --format '{{.Size}} bytes'
```

`server` is a distroless image with the static `cmd/server` binary, the small
static `/app/healthcheck` probe, `api-docs/` (Swagger UI), `migrations/` and a
writable `logs/` directory. Agent prompts are Go-embedded at compile time, so
no prompt files are needed at runtime. Goose itself only ships in the `migrate`
image; nginx/Node only in the `web` image.

## Migrations (two goose groups, applied automatically)

`docker compose --profile server up -d --build server web-server` starts the
migration chain first (after Postgres is healthy):

1. `migrate` applies every pending migration from `/migrations` using goose's
   main version table (`goose_db_version`);
2. `migrate-vector` then applies `/migrations_optional/vector` using a
   **separate** version table (`goose_vector_versions`), so the two histories
   stay independent. The vector SQL references tables created by the main group
   (`sources`, `run_sources`) and needs the `vector` extension, hence the order;
3. only when both one-shot containers exit `0` does compose start `server`
   (`depends_on: condition: service_completed_successfully`).

Consequences:

* a non-zero exit from either runner fails the `up` and the backend **never
  starts**, so a stale/incomplete schema is never served silently. The
  backend's own startup schema check (`SSE events` + `run_chat_messages` +
  `runs.stage/type` + `evidence.ref_id`) is a second safety net;
* both runs are idempotent: a re-run on a current DB applies nothing;
* a fresh database (and an already-migrated one) ends up with **all** groups,
  including the optional vector tables and index.

A fresh DB gets, among others:

| Object | Source |
| ------ | ------ |
| `source_fragments`, `fragment_embeddings` | `migrations_optional/vector/20261109010000_vector_retrieval.sql` |
| `idx_fragment_embeddings_cosine` HNSW cosine index on `fragment_embeddings.embedding` | same migration |
| `find_relevant_fragments(...)` (vector search) | same migration |
| `find_fragments_bm25(...)` (lexical fallback) | `migrations_optional/vector/20261109010001_lexical_fallback.sql` |
| `goose_vector_versions` rows = applied vector migrations | goose bookkeeping |

Manual operations:

```bash
# Main group (idempotent; already-applied versions are skipped)
docker compose --profile server run --rm migrate up

# Main group status
docker compose --profile server run --rm migrate status

# Optional vector group status (the -table/-dir flags are required for status:
# the container's baked command already carries them for `up`)
docker compose --profile server run --rm migrate-vector \
  -table goose_vector_versions -dir /migrations_optional/vector status

# Roll back the last main migration (destructive; see migrations/README.md)
docker compose --profile server run --rm migrate down
```

Inspect the result directly:

```bash
docker compose --profile server exec -T postgres \
  psql -U activity -d activity_descriptor -c "\dt source_fragments fragment_embeddings"
docker compose --profile server exec -T postgres \
  psql -U activity -d activity_descriptor -c "SELECT indexdef FROM pg_indexes WHERE indexname='idx_fragment_embeddings_cosine';"
docker compose --profile server exec -T postgres \
  psql -U activity -d activity_descriptor -c "SELECT version_id FROM goose_vector_versions ORDER BY version_id;"
```

The same two commands are used by the CI `db-integration` job (`goose ... up` and
`goose -table goose_vector_versions -dir ... up`). See
[migrations/README.md](../migrations/README.md) and
[VECTOR_STORAGE_GUIDE.md](../migrations_optional/vector/VECTOR_STORAGE_GUIDE.md).

### Migrating an existing database

The migration services point at the compose `postgres` service by default. To
run migrations and the backend against another PostgreSQL, set
`SERVER_DATABASE_URL` (it must be reachable **from the container**; a host
`localhost` is the container itself):

```bash
# PostgreSQL on the Docker host (Docker Desktop / Linux with host-gateway):
export SERVER_DATABASE_URL='postgres://activity:activity_dev_password@host.docker.internal:5432/activity_descriptor?sslmode=disable'
docker compose --profile server up -d --build server web-server
```

The compose services map `host.docker.internal` to the host gateway, so this
also works on Linux. For the local Postgres container that the native backend
uses, the same DSN via `host.docker.internal:5432` applies as long as that
container is running.

## Environment and secrets

Precedence for the `server` container: compose `environment` (explicit) >
`env_file` entries (later file wins) > image defaults. The `web-server` service
receives no secrets: it proxies to `server`.

| Source | Where | What |
| ------ | ----- | ---- |
| `.env` (git-ignored) | auto-loaded by compose for interpolation **and** passed via `env_file` | `HH_*` credentials, optional `JWT_SECRET` / `LLM_*`, ports (`SERVER_PORT`, `SERVER_WEB_PORT`, `SERVER_DATABASE_URL`, `POSTGRES_*`) |
| `.secrets/llm.env` (git-ignored) | `env_file` | `JWT_SECRET`, `LLM_API_KEY`, `LLM_BASE_URL`, `LLM_MODEL`, per-agent overrides |
| Shell environment | compose interpolation | `SERVER_PORT`, `SERVER_WEB_PORT`, `SERVER_DATABASE_URL`, `APP_ENV`, `LOG_LEVEL`, `POSTGRES_*` |

`.env` and `.secrets/llm.env` are optional in the compose file
(`required: false`), so `docker compose config` passes in CI where neither file
is checked out; `.env.example` documents the shared backend/provider variables.

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
  With a provider configured, the `/metrics` exposition also includes the
  agent/LLM families (see the monitoring section).
* `OTEL_EXPORTER_OTLP_ENDPOINT` — empty disables trace export; set
  `http://jaeger:4318` with the monitoring profile for the local collector.
* `HH_*`, `OLLAMA_*`, `CORS_ALLOWED_ORIGINS`, `SSE_HEARTBEAT_S`, `LOG_LEVEL`,
  `LOG_FILE` — passed through the same env files when needed.

Nothing is baked into the images; `.dockerignore` keeps `.env`, `.secrets/`,
`frontend/node_modules`, `frontend/dist`, `frontend/logs` and other local data
out of the build context.

## Health checks

```bash
# Compose health status (web-server healthy once GET /healthz returns 200;
# server healthy once /api/v1/health returns 2xx).
docker compose --profile server ps
docker inspect --format '{{.Name}} {{.State.Health.Status}}' activity-descriptor-server-1
docker inspect --format '{{.Name}} {{.State.Health.Status}}' activity-descriptor-web-server-1

# Manual probes through the single public port
curl -fsS -o /dev/null -w 'spa: %{http_code}\n'     http://127.0.0.1:8083/
curl -fsS -o /dev/null -w 'health: %{http_code}\n'  http://127.0.0.1:8083/api/v1/health
curl -fsS -o /dev/null -w 'swagger: %{http_code}\n' http://127.0.0.1:8083/swagger/
curl -fsS -o /dev/null -w 'swagger spec: %{http_code}\n' http://127.0.0.1:8083/swagger/swagger.json

# Same probe the backend container runs (distroless: no shell/curl)
docker exec activity-descriptor-server-1 /app/healthcheck
```

Container logs go to stdout (`docker compose --profile server logs -f server
web-server migrate migrate-vector`) and to the in-container rotating file
`/app/logs/app.log` (`LOG_FILE` overrides it). The Swagger UI is served from
`./api-docs` relative to the container working directory `/app`; nginx proxies
it (the Swagger page itself loads its JS/CSS from the unpkg CDN, so the browser
needs internet access for the UI; the spec is local).

## JWT-protected requests

Tokens must be signed with the same `JWT_SECRET` the server uses. The demo api
image already ships `/local-token`; use it as a small token issuer:

```bash
# Read the secret from your local env file without printing it.
JWT_SECRET="$(grep -E '^JWT_SECRET=' .secrets/llm.env | cut -d= -f2-)"   # or export it yourself
TOKEN="$(docker run --rm -e JWT_SECRET="$JWT_SECRET" \
  --entrypoint /local-token activity-descriptor-api:local -role admin)"

# Through the single public entry point (nginx -> server):
curl -fsS -X POST http://127.0.0.1:8083/api/v1/runs \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"profession":"data analyst"}'

curl -fsS http://127.0.0.1:8083/api/v1/runs/<run-id> -H "Authorization: Bearer $TOKEN"
```

Without a valid token the protected routes return 401. With `LLM_*` configured,
creating a run can make real provider calls (paid); without them the fallback
pipeline is used.

## Monitoring the full backend (Prometheus / Grafana)

The monitoring profile has three scrape modes, selected with
`PROMETHEUS_CONFIG` (all files are mounted from `deploy/prometheus`):

| `PROMETHEUS_CONFIG` | Target | When |
| ------------------- | ------ | ---- |
| `prometheus.yml` (default) | `api:8080` (demo compose api) | default demo stack |
| `prometheus.host.yml` | `host.docker.internal:8080` (backend on the Docker host) | `make run` / `go run ./cmd/server` |
| **`prometheus.server.yml`** | **`server:8080` (compose `server` profile)** | full backend in compose |

`prometheus.server.yml` scrapes the real backend's `/metrics`, so the same
exposition now feeds the dashboards with:

* **HTTP metrics** — `activity_descriptor_http_requests_total`,
  `activity_descriptor_http_request_duration_seconds`,
  `activity_descriptor_http_in_flight_requests`, `..._runs_total`;
* **agent/LLM metrics** (when an LLM provider is configured) —
  `activity_descriptor_agent_calls_total`, `..._agent_errors_total`,
  `..._agent_tokens_total`, `..._agent_latency_seconds`,
  `..._agent_reasoning_total`.

The scrape job is named `activity-descriptor-api-server`, which keeps the
`job=~"activity-descriptor-api.*"` selectors in
[`deploy/prometheus/alerts.yml`](../deploy/prometheus/alerts.yml) and the
provisioned dashboards working unchanged for the full backend; the
`deployment="compose-server"` label distinguishes it from the demo/host modes.
Neither the backend nor the dashboards use run-level identifiers as metric
labels.

```bash
# 1) full stack first (single public URL http://localhost:8083)
docker compose --profile server up -d --build server web-server

# 2) monitoring against it. --no-deps is REQUIRED: without it prometheus'
#    depends_on starts the demo `api`, which wants host port 8080 (owned by the
#    host backend in the local demo).
PROMETHEUS_CONFIG=prometheus.server.yml \
  docker compose --profile monitoring up -d --no-deps prometheus grafana jaeger

# Verify the scrape target is UP and metrics are ingested.
curl -fsS http://127.0.0.1:9090/api/v1/targets | grep -o '"job":"activity-descriptor-api-server"[^}]*"health":"[a-z]*"'
curl -fsS 'http://127.0.0.1:9090/api/v1/query?query=activity_descriptor_http_requests_total'
```

Grafana: <http://localhost:3000> (login required) with the provisioned
`activity-descriptor` dashboard (HTTP) and `activity-descriptor-business`
dashboard (HTTP + LLM/agent/run panels). Agent/LLM series appear after the first
LLM-backed request; before any traffic the panels show a "no traffic yet" state,
not an error. To switch back: stop the `server`/`web-server` services and start
monitoring again without `PROMETHEUS_CONFIG` (default `prometheus.yml`).

## Switching between demo api and full server

| Goal | Command | Host URL |
| ---- | ------- | -------- |
| Demo api + web + postgres | `docker compose up -d --build api web postgres` | http://localhost:8080 (api), http://localhost:8081 (web placeholder) |
| Full stack: backend + migrations + SPA/nginx | `docker compose --profile server up -d --build server web-server` | **http://localhost:8083** |
| Full stack (already built) | `docker compose --profile server up -d server web-server` | http://localhost:8083 |
| Direct backend only (already built) | `docker compose --profile server up -d server` | http://localhost:8082 |
| Stop the full stack | `docker compose --profile server stop server web-server` | — |
| Stop/remove the full-stack services | `docker compose --profile server rm -f migrate migrate-vector server web-server` | — |
| Full stack + monitoring | `make up-server-monitoring` | http://localhost:8083, :3000, :9090 |
| Stop the whole project (keeps volumes) | `docker compose --profile server --profile monitoring --profile automation down` | — |

Both stacks can run at the same time: they use different host ports and the
demo api keeps its in-memory store, while `server` uses the shared compose
PostgreSQL volume. Migrations only touch that PostgreSQL, never the demo api.

## Verification evidence (this worktree, 2026-11-19)

Executed with Docker 29.9 / Compose v5.6.1 reachable. Every runtime check used
an **isolated compose project and host ports** (`-p cga-fullstack`,
`SERVER_PORT=18082`, `SERVER_WEB_PORT=18083`, `POSTGRES_PORT=55432`,
`PROMETHEUS_PORT=19090`, `GRAFANA_PORT=13000`), so the demo stack and the host
backend on :8080 were never touched. All test containers, networks, volumes and
compose projects were removed afterwards; the pre-existing containers
(`n8ngmail` n8n/postfix/postgres) were re-checked as still running.

| Check | Command | Result |
| ----- | ------- | ------ |
| deploy assets | `python scripts/check_deploy.py` | `deploy config OK` (Dockerfile/stages, compose, nginx, 3 Prometheus modes, 2 dashboards x metrics sources, no run_id label, secrets scan) |
| compose config | `docker compose config --quiet`; `--profile server`; `--profile monitoring --profile automation`; `--profile server --profile monitoring` | all exit 0 |
| images built | `docker build --target server/migrate/web`; `docker build .` (default api) | server **42,874,863 B**, migrate **22,910,944 B**, web **75,735,573 B**, demo api **27,091,401 B** (< 100 MB CI budget) |
| fresh DB, both goose groups | compose `up` (isolated project) | Postgres healthy -> `migrate` exit 0 (**19** main migrations, max `20261119010000`) -> `migrate-vector` exit 0 (**2** vector migrations) -> `server` healthy -> `web-server` healthy |
| vector schema | psql: `\dt source_fragments fragment_embeddings` | both tables present (33 public tables) |
| HNSW index | psql: `SELECT indexdef FROM pg_indexes WHERE indexname='idx_fragment_embeddings_cosine'` | `CREATE INDEX ... USING hnsw (embedding vector_cosine_ops)` |
| vector functions | psql: `to_regprocedure('find_relevant_fragments(uuid,vector,text,integer)')`, `...find_fragments_bm25...` | both present |
| vector idempotency | `run --rm migrate-vector` again + `migrate status` | `goose: no migrations to run. current version: 20261109010001`; vector rows stay 2, main rows stay 19 |
| migration failure gate | compose `up` with a bad DB password (`SERVER_DATABASE_URL`) | `up` exit 1, `migrate` `Exited (1)` (SASL auth failure), `server`/`web-server` `Created` but **never started** |
| single public entry point | `GET :18083/`, `/healthz`, `/api/v1/health`, `/swagger/`, `/swagger/swagger.json`, `/metrics` | 200 / 200 / 200 (`{"status":"ok"}`) / 200 (`swagger-ui` page) / 200 (spec) / 200 (Prometheus text) |
| SPA fallback | `GET :18083/runs/does-not-exist` | 200 + `index.html` (`<div id="root">`) |
| auth through nginx | `GET :18083/api/v1/runs` without a token | 401 (backend reached through the proxy) |
| direct backend | `GET :18082/api/v1/health`, `/metrics` | 200 / 200, `activity_descriptor_http_*` and agent/LLM family definitions present |
| metrics via nginx | `GET :18083/metrics` | 200, HTTP counters for `/api/v1/health`, `/swagger/`, `/metrics`, `/api/v1/runs`; **no run_id label** in the exposition |
| Prometheus scrape target | `GET :19090/api/v1/targets` with `PROMETHEUS_CONFIG=prometheus.server.yml` | job `activity-descriptor-api-server`, instance `server:8080`, `deployment="compose-server"`, **health=up**; `up{job="activity-descriptor-api-server"} = 1` |
| Prometheus ingestion | `GET :19090/api/v1/query?query=activity_descriptor_http_requests_total` | `status=success`, 4 series (routes `/api/v1/health` 200, `/api/v1/runs` 401, `/metrics` 200, `/swagger/` 200) |
| Grafana dashboard data path | Grafana API with admin basic auth: `GET /api/dashboards/uid/activity-descriptor-business`, `GET /api/datasources/proxy/uid/prometheus/api/v1/query?...` | Grafana healthy, dashboard provisioned (14 panels), datasource query `status=success` with 4 HTTP series; agent/LLM panels defined and their families cross-checked against `internal/llm/metrics` (series appear after LLM traffic) |
| demo default stack | `docker compose up -d --build api web postgres` (isolated ports) | api `/health` 200, `/metrics` 200, placeholder `web` `/` 200, `web` `/api/v1/health` 200, `/healthz` 200 — unchanged by the new profile services |

Note: `docker compose config` renders the contents of the local `env_file`
entries when they exist, so its output can contain secrets locally — do not
paste it into reports. In CI neither file is checked out, so the rendered
config is secret-free.

## Troubleshooting

| Symptom | Cause / fix |
| ------- | ----------- |
| `server` exits with `DATABASE_URL and JWT_SECRET are required` | Add `JWT_SECRET` (and optionally `DATABASE_URL`) to `.env` or `.secrets/llm.env`. Both env files are resolved relative to the repository root. |
| `server` exits with `база не подготовлена: примените основные миграции...` | Migrations are missing/older than the code: `docker compose --profile server run --rm migrate up`, then restart `server`. |
| `migrate` / `migrate-vector` exits non-zero | Check `docker compose --profile server logs migrate migrate-vector`; fix the DSN/DB and re-run. The backend correctly refuses to start in this case. |
| `server` unhealthy / no HTTP | `docker compose --profile server logs server`; the startup order is Postgres healthy -> `migrate` exit 0 -> `migrate-vector` exit 0 -> `server`. |
| `web-server` healthy but `/api/...` returns 502 | nginx cannot reach `server` yet or it crashed: `docker compose --profile server ps` / `logs server`. nginx `proxy_pass http://server:8080` requires the `server` container on the same compose network. |
| SPA route 404 after refresh | The image was built with an old nginx config: rebuild `web-server` (`docker compose --profile server build web-server`). The `try_files $uri $uri/ /index.html` fallback is baked into the image. |
| `web` image build fails at `npm ci` | Registry/network problem in the Docker build. Reproduce with `cd frontend && npm ci && npm run build`; CI `frontend` job runs exactly these steps. |
| Frontend loads but API calls fail | Check the browser network tab: calls must be relative `/api/v1/...` through port 8083. A build with a hard-coded `VITE_API_URL` pointing at the wrong origin breaks the same-origin setup. |
| Swagger UI page empty | The page loads swagger-ui JS/CSS from unpkg CDN (browser needs internet); the spec itself is served locally at `/swagger/swagger.json`. |
| Agent/LLM metric panels are empty | No LLM provider configured or no LLM-backed request yet: `agent_*` series only appear after the first agent call; HTTP panels are populated immediately. |
| Prometheus target `api:8080` is DOWN in server mode | Wrong config: start monitoring with `PROMETHEUS_CONFIG=prometheus.server.yml` and `--no-deps` (the default config scrapes the demo api, which is not part of this stack). |
| `bind: address already in use` on start | Host port taken: set `SERVER_PORT` (e.g. `8083`) / `SERVER_WEB_PORT` (e.g. `8084`) in `.env`. |
| Protected route returns 401 | Token was issued with a different `JWT_SECRET`; re-issue with the same secret the compose profile uses. |
| `invalid PostgreSQL configuration` / `cannot connect to PostgreSQL` | `SERVER_DATABASE_URL` points at an unreachable host, or at a host `localhost` from inside the container. Use the compose service name or `host.docker.internal`. |
| `docker compose config` fails in CI | Should not happen: the optional env files use `required: false`. Run `docker compose config --quiet` locally to reproduce. |
