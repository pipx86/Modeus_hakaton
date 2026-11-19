# Docker: image, compose stack and local runbook

> Status: verified locally in this worktree. The default `api` image builds
> (27.0 MB, well below the 100 MB CI budget), the demo container smoke passes
> (`/health`, `/metrics`, `/api/v1/health`, self-healthcheck, JWT chat flow) and
> the full `server` profile was built, migrated (main **and** optional vector
> goose groups) and smoke-tested end-to-end against an isolated PostgreSQL,
> including the built React frontend served by nginx on a single entry point
> (see [DOCKER_SERVER.md](DOCKER_SERVER.md) and "Acceptance evidence" below). CI
> `docker` job remains the authoritative gate; it builds the default image
> without `--target`.

## Services

| Service | Image / build | Profile | Host port | Healthcheck |
| ------- | ------------- | ------- | --------- | ----------- |
| `api` | built from `Dockerfile` (default target) | default | 8080 | `/api -healthcheck` (self-probe) |
| `web` | built from `frontend/Dockerfile` (Vite + nginx) | default | 8081 | `GET /healthz` |
| `postgres` | `pgvector/pgvector:0.8.0-pg16` | default | 5432 | `pg_isready` |
| `migrate` | built from `Dockerfile` (`migrate` target) | `server` | — | exit code (`service_completed_successfully`) |
| `migrate-vector` | built from `Dockerfile` (`migrate` target) | `server` | — | exit code (`service_completed_successfully`) |
| `server` | built from `Dockerfile` (`server` target) | `server` | 8082 (`SERVER_PORT`) | `/app/healthcheck` -> `GET /api/v1/health` |
| `web-server` | built from `Dockerfile` (`web` target: React build + nginx) | `server` | 8083 (`SERVER_WEB_PORT`) | `GET /healthz` |
| `prometheus` | `prom/prometheus:v2.54.1` | `monitoring` | 9090 | — |
| `grafana` | `grafana/grafana:11.2.0` | `monitoring` | 3000 | — |
| `n8n` | `n8nio/n8n:1.63.4` | `automation` | 5678 | `GET /healthz` |
| `n8n-postgres` | `postgres:16-alpine` | `automation` | internal | `pg_isready` |

`postgres` uses the pgvector image because the backend migrations add the
`vector` extension; the demo API does not use it.

## Images and build targets

`Dockerfile` is multi-stage; the **last stage stays the demo `api` image**, so
`docker build .` (the CI job) is unchanged:

| Target | Contents |
| ------ | -------- |
| `api` (default) | `cmd/api` + `cmd/local-token`, distroless, no shell |
| `server` | `cmd/server`, `/app/healthcheck`, `api-docs/` (Swagger), `migrations*/`, distroless |
| `migrate` | pinned goose v3.29.0 + both migration directories (used by `migrate` and `migrate-vector`) |
| `web` | production `frontend/dist` built with `npm ci && npm run build` + nginx + `deploy/nginx/server.conf` |

The `frontend-build` stage (`node:22.20-bookworm-slim`) builds the React SPA
inside Docker; the final `web` image is nginx:1.27-alpine plus static files and
the reverse-proxy config (no Node runtime). `web-server` serves the SPA and
proxies `/api/`, `/swagger/` and `/metrics` to `server` on one origin/port, so
the full stack has a single public URL and no CORS setup.

The demo runtime layer is the ~2.5 MB distroless base plus two stripped Go
binaries; the expected image is **well below the 100 MB budget** (~20 MB). There
is no shell, package manager or libc. The container healthcheck is `/api
-healthcheck`: distroless has no shell/curl, so the binary probes itself.

`/local-token` is included in the image so a local demo can issue a JWT for the
JWT-protected `/api/v1` endpoints with `docker compose exec api /local-token`.

## Demo API vs production server

| Command | Purpose | Storage | Auth | DB |
| ------- | ------- | ------- | ---- | -- |
| `cmd/api` (image entrypoint, compose `api`) | demo stack: runs without goose/LLM | in-memory | JWT | none |
| `cmd/server` | production backend owner | PostgreSQL | JWT | required |

The demo API exposes `GET /health` (public), `GET /metrics` (public),
`GET /api/v1/health` (public, from the backend router) and the existing
`/api/v1/*` REST/chat endpoints (JWT protected). The interviewer chat uses
deterministic fallback questions (`chat.UnavailableClient`); no LLM calls and
no provider keys are needed.

## Full backend profile (`cmd/server`)

The demo image above stays the default. To run the real PostgreSQL backend and
the built frontend through a single nginx entry point, use the `server` compose
profile:

```bash
cp .env.example .env        # set JWT_SECRET (>= 32 bytes); never commit .env
docker compose --profile server up -d --build server web-server
curl -fsS http://localhost:8083/api/v1/health     # API through nginx
open http://localhost:8083                        # built React SPA (same origin)
curl -fsS http://localhost:8082/api/v1/health     # direct backend (optional)
```

The profile builds the `server`, `migrate` and `web` Dockerfile targets. Two
one-shot goose services run first: `migrate` applies `./migrations` (main
history) and `migrate-vector` applies `./migrations_optional/vector` with its own
`goose_vector_versions` table; the backend only starts after both exit 0. Then
`web-server` serves `frontend/dist` (built with `npm ci && npm run build` inside
the image) and proxies `/api/`, `/swagger/` and `/metrics` to `server`, so the
full stack has one public port and no CORS. Secrets come from the git-ignored
`.env` / `.secrets/llm.env`; nothing is baked into the image. The full runbook
(env precedence, migrations incl. the vector group, monitoring with the
`server` scrape config, JWT requests, switching between demo and full backend)
is [DOCKER_SERVER.md](DOCKER_SERVER.md).

## First run

```bash
cd <repository root>                     # in this worktree: .worktrees/roman-ai
cp .env.example .env                     # set JWT_SECRET etc.; .env is git-ignored
docker compose up -d --build api web postgres

curl -fsS http://localhost:8080/health
curl -fsS http://localhost:8080/metrics | head
curl -fsS http://localhost:8080/api/v1/health
open http://localhost:8081               # собранный frontend
```

JWT demo (chat endpoints):

```bash
TOKEN=$(docker compose exec -T api /local-token -role admin)
curl -fsS -X POST http://localhost:8080/api/v1/runs \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"profession":"аналитик данных"}'
curl -fsS http://localhost:8080/api/v1/runs/run-1/messages \
  -H "Authorization: Bearer $TOKEN"
```

Optional profiles:

```bash
make up-monitoring    # prometheus + grafana  -> http://localhost:3000
make up-automation    # n8n + n8n-postgres    -> http://localhost:5678
```

Stop everything (add `-v` to remove volumes):

```bash
docker compose --profile monitoring --profile automation --profile server down
```

## Local run without Docker

```bash
make run        # HTTP_ADDR=127.0.0.1:8080, JWT_SECRET from .env or a local fallback
make token      # prints a JWT for curl
make demo-smoke # builds binaries and curls /health, /metrics and the JWT chat flow
make e2e        # offline interview → analysis → synthesis pipeline (fake model, no DB)
```

These targets were executed natively in this session and passed:
`/health` 200, `/metrics` 200 (`text/plain; version=0.0.4`, metric families
present, no run identifiers), `/api/v1/health` 200, unauthenticated
`POST /api/v1/runs` 401, JWT create-run 201, fallback interviewer message
returned, `POST .../messages` 201, `-healthcheck` exit 0 when up and exit 1
when down.

## Acceptance evidence (verified locally in this worktree)

```bash
docker build -t activity-descriptor-api:local .                       # default target
docker image inspect activity-descriptor-api:local --format '{{.Size}} bytes'
# -> 27091401 bytes (< 104857600, the CI budget)

docker run -d --name api-smoke -p 127.0.0.1:18093:8080 \
  -e JWT_SECRET=ci-demo-jwt-secret-at-least-32-bytes-long activity-descriptor-api:local
curl -fsS http://127.0.0.1:18093/health          # 200 {"mode":"demo","store":"in-memory"}
curl -fsS http://127.0.0.1:18093/metrics         # 200, activity_descriptor_* families
curl -fsS http://127.0.0.1:18093/api/v1/health   # 200 {"status":"ok"}
docker exec api-smoke /api -healthcheck          # exit 0
docker exec api-smoke /local-token -role admin   # JWT
# JWT POST /api/v1/runs -> 201 run-1; GET .../messages -> fallback agent messages
```

The default demo stack was also re-verified in an isolated compose project
after the profile changes (`api`, `web`, `postgres` on spare ports): api
`/health` 200, `/metrics` 200, web placeholder `/` 200, web `/api/v1/health`
proxy 200, `/healthz` 200.

The full `cmd/server` backend (server + migrate targets, PostgreSQL, both goose
groups, Swagger, JWT) plus the built frontend (`web` target) and the monitoring
scrape config has its own verified runbook with per-check evidence:
[DOCKER_SERVER.md](DOCKER_SERVER.md).

## Secrets

* Only environment variables are used; nothing is baked into the image.
* `.env.example` documents the shared variables; `.env` is git-ignored. The
  profile's `SERVER_WEB_PORT` (single public web port, default 8083) has a
  compose default, so it only needs to be set when 8083 is taken.
* `JWT_SECRET` must be a real secret (>= 32 bytes) outside local dev; compose
  falls back to a documented local placeholder.
* The full `server` profile has **no** JWT placeholder: it reads
  `JWT_SECRET`/`LLM_*` from the git-ignored `.env` and `.secrets/llm.env`
  (optional `env_file` entries), so the backend refuses to start without a
  secret instead of starting on a known one. See [DOCKER_SERVER.md](DOCKER_SERVER.md).
* `OPENROUTER_API_KEY_FILE` points at a key file for `cmd/server`; the demo API
  never reads it and never calls an LLM. Keep the key in the git-ignored
  `.secrets/` directory, never in git or in the image.
* Defaults such as `activity_dev_password` / `admin_dev_password` are local
  placeholders only.

## Troubleshooting

* **`permission denied ... dockerDesktopLinuxEngine`** — Docker Desktop is not
  running or the current user/session cannot access its named pipe. Start
  Docker Desktop / check `docker context ls`; compose `config` works without
  the daemon.
* **API unhealthy** — `docker compose logs api`; run
  `docker compose exec api /api -healthcheck` manually.
* **Port already allocated** — override `API_PORT`, `WEB_PORT`, `SERVER_PORT`,
  `SERVER_WEB_PORT`, `POSTGRES_PORT`, `GRAFANA_PORT`, `N8N_PORT` in `.env`.
* **Full backend (`server` profile) issues** — see
  [DOCKER_SERVER.md](DOCKER_SERVER.md): missing `JWT_SECRET`, migration failures
  and unreachable database DSNs all fail fast with a documented message.
* **Protected endpoint returns 401** — `JWT_SECRET` is unset/empty or the token
  was issued with a different secret; regenerate with `/local-token`.
