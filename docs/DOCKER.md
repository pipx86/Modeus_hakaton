# Docker: image, compose stack and local runbook

> Status in the authoring session: the Docker **daemon is not reachable** from
> the sandbox (`docker info` / `docker build` fail: named-pipe access denied to
> `dockerDesktopLinuxEngine`). The image was **not built** and the stack was
> **not started** here. `docker compose config` is client-side and passes for
> the default services and for both profiles; the local API was smoke-tested
> natively (see "Local run without Docker"). The CI `docker` job builds the
> image, asserts `< 100 MB` and runs the container smoke test.

## Services

| Service | Image / build | Profile | Host port | Healthcheck |
| ------- | ------------- | ------- | --------- | ----------- |
| `api` | built from `Dockerfile` | default | 8080 | `/api -healthcheck` (self-probe) |
| `web` | `nginx:1.27-alpine` (static placeholder) | default | 8081 | `GET /healthz` |
| `postgres` | `pgvector/pgvector:0.8.0-pg16` | default | 5432 | `pg_isready` |
| `prometheus` | `prom/prometheus:v2.54.1` | `monitoring` | 9090 | — |
| `grafana` | `grafana/grafana:11.2.0` | `monitoring` | 3000 | — |
| `n8n` | `n8nio/n8n:1.63.4` | `automation` | 5678 | `GET /healthz` |
| `n8n-postgres` | `postgres:16-alpine` | `automation` | internal | `pg_isready` |

`postgres` uses the pgvector image because the backend migrations add the
`vector` extension; the demo API does not use it.

## API image

`Dockerfile` is a two-stage build:

1. `golang:1.26.10-bookworm` compiles `cmd/api` and `cmd/local-token` with
   `CGO_ENABLED=0`, `-trimpath -ldflags="-s -w"`, copying `cmd/`, `internal/`
   and the embedded `prompts/`.
2. `gcr.io/distroless/static-debian12:nonroot` runs as `nonroot:nonroot`.

The runtime layer is the ~2.5 MB distroless base plus two stripped Go binaries;
the expected image is **well below the 100 MB budget** (~20 MB). There is no
shell, package manager or libc. The container healthcheck is `/api
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

## First run

```bash
cd .worktrees/devops
cp .env.example .env                     # set JWT_SECRET etc.; .env is git-ignored
docker compose up -d --build api web postgres

curl -fsS http://localhost:8080/health
curl -fsS http://localhost:8080/metrics | head
curl -fsS http://localhost:8080/api/v1/health
open http://localhost:8081               # static web placeholder
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
docker compose --profile monitoring --profile automation down
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

## Acceptance evidence to run with Docker

```bash
docker build -t activity-descriptor-api:local .
docker image inspect activity-descriptor-api:local --format '{{.Size}} bytes'
test "$(docker image inspect activity-descriptor-api:local --format '{{.Size}}')" -lt 104857600

docker compose up -d --build api web postgres
docker compose ps
curl -fsS http://localhost:8080/health
curl -fsS http://localhost:8080/metrics
docker compose exec api /api -healthcheck          # exit 0
```

## Secrets

* Only environment variables are used; nothing is baked into the image.
* `.env.example` documents every variable; `.env` is git-ignored.
* `JWT_SECRET` must be a real secret (>= 32 bytes) outside local dev; compose
  falls back to a documented local placeholder.
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
* **Port already allocated** — override `API_PORT`, `WEB_PORT`,
  `POSTGRES_PORT`, `GRAFANA_PORT`, `N8N_PORT` in `.env`.
* **Protected endpoint returns 401** — `JWT_SECRET` is unset/empty or the token
  was issued with a different secret; regenerate with `/local-token`.
