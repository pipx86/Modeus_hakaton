# CI

The DevOps workflow is [`.github/workflows/ci.yml`](../.github/workflows/ci.yml).
The existing [`.github/workflows/backend-acceptance.yml`](../.github/workflows/backend-acceptance.yml)
is **unchanged** and remains the owner of the full PostgreSQL acceptance run
(DB branch migrations + seed + data-quality checks). This workflow adds
DevOps/lint/build/image gates without duplicating that contract.

Go is pinned to **1.26.10** in CI (`actions/setup-go` `go-version: "1.26.10"`),
matching `go.mod` and the Dockerfile builder (`golang:1.26.10-bookworm`).

## Jobs

| Job | What it proves |
| --- | -------------- |
| `lint` | `gofmt -l cmd internal` empty, `go vet ./...`, `golangci-lint` (v1.62.2, `.golangci.yml`) |
| `test` | `go build ./...` and `go test -race -count=1 ./...` (no DB/LLM required on this branch) |
| `e2e` | offline `cmd/e2e` pipeline (fake deterministic model, no network, no DB) produces `e2e-output.json`; artifact uploaded |
| `db-integration` | Postgres 16 + pgvector service, best-effort checkout of the DB migrations branch (`feature/rustam/goose-migrations`), `goose up`, schema assertion, then tests with `DATABASE_URL` |
| `deploy-config` | `scripts/check_deploy.py` (Dockerfile/stages incl. the frontend + nginx web target, compose incl. the `migrate-vector`/`web-server` chain, the three Prometheus scrape modes, both Grafana dashboards cross-checked against the Go metrics sources with no `run_id` label, vector migrations, n8n, docs/secrets), `docker compose config --quiet` for default + `server` + `monitoring` + `automation` profiles, required deliverables |
| `frontend` | runs only if `frontend/package.json` exists; `npm ci`, lint, typecheck, tests and `npm run build` — the same steps the Docker `frontend-build` stage runs for the `web` image |
| `docker` | builds the default image, asserts **< 100 MB**, runs the container and curls `/health`, `/metrics`, `/api/v1/health`, the self-healthcheck and the JWT chat flow via `/local-token` |

## Migrations in CI

Migrations live in the DB workstream branch, so `db-integration` fetches it
best-effort:

* `actions/checkout` of `feature/rustam/goose-migrations` into `.ci-db` with
  `continue-on-error: true`;
* if `.ci-db/migrations` (or a repo-relative `migrations/`) exists, goose
  applies both migration groups and the job asserts `public.runs` exists;
* otherwise the step emits a warning — the separate `backend-acceptance.yml`
  still owns full DB acceptance, and this branch must stay green before the
  migrations merge.

Adjust the branch name in the workflow once the migrations land on a stable
branch.

## Run the same checks locally

```bash
make ci             # fmt-check + vet + test
make test-race
make e2e
make check-compose  # docker compose config (no daemon needed)
make check-deploy   # Python validation of all deploy assets (needs PyYAML)
make demo-smoke     # native API + JWT chat + metrics smoke (no Docker)
```

`golangci-lint` is optional locally:

```bash
go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.62.2
make lint
```

## Docker in CI vs this session

The CI `docker` job is the authoritative Docker evidence: image size assertion,
container healthcheck and the JWT chat smoke test. It builds the **default**
image target without `--target`, which stays the demo `api` image. The
`server` profile's `server`, `migrate` and `web` targets are **not** built by
this job (they would add a PostgreSQL service plus a Node build to every run);
they are covered locally and documented in [DOCKER_SERVER.md](DOCKER_SERVER.md).

Locally (2026-11-19, Docker Desktop reachable) the default image was built and
smoke-tested (27.1 MB, `/health`, `/metrics`, `/api/v1/health`, self-healthcheck,
JWT chat flow), and the full `cmd/server` stack was verified end-to-end through
the `server` compose profile on an isolated project: both goose groups
(main + optional vector with a separate version table), pgvector tables and HNSW
index, server healthcheck, Swagger UI, JWT-protected run, the SPA/nginx
single-origin entry point, a migration-failure gate and Prometheus scraping
`server:8080` with the dashboards' HTTP/agent/LLM families. Details:
[DOCKER_SERVER.md](DOCKER_SERVER.md). `scripts/check_deploy.py` also validates
`docker compose --profile server config`, the nginx full-stack config, the
`prometheus.server.yml` scrape mode and the vector migration chain.

## Limitations

* No real LLM calls: `cmd/e2e` uses a deterministic fake model, and the demo API
  uses the interviewer fallback. Provider keys are never available in CI.
* The `db-integration` job does not replace `backend-acceptance.yml`; it is a
  lightweight smoke check that stays green while migrations are in another
  branch.
* No browser/e2e HTTP test of the frontend: the `frontend` job runs
  lint/typecheck/tests/build and the SPA + nginx proxy path was smoke-tested
  locally (single origin: `/`, `/api/v1/health`, `/swagger/`, `/metrics`), but
  CI does not run a browser. The static demo placeholder HTML is still validated
  by `scripts/check_deploy.py`.
* The `docker` job does not build the `server`/`migrate`/`web` targets or start a
  PostgreSQL service; the full `server` profile is verified locally (see
  [DOCKER_SERVER.md](DOCKER_SERVER.md)) and statically by `check_deploy.py`.

## GitHub Actions billing block and local CI

On 2026-11-19 GitHub Actions stopped starting every job (CI, acceptance and db-check) with:

```text
The job was not started because recent account payments have failed
or your spending limit needs to be increased.
Please check the 'Billing & plans' section in your settings.
```

The annotation is attached to each job; `runner_name` is empty and the jobs
finish in 2–4 seconds without logs. This is **not a code/test failure**. GitHub
Status reported all systems operational. Fixing it requires the repository owner
to update the payment method or Actions spending limit in
`Settings -> Billing and plans -> Actions`, then re-run the failed jobs.

While Actions is blocked, run the same important gates locally:

```powershell
pwsh -ExecutionPolicy Bypass -File scripts/ci-local.ps1
```

The script runs:

```text
go build ./...
go vet ./...
go test ./...
git diff --check
python scripts/check_deploy.py
docker compose config --quiet (default + server + monitoring/automation)
frontend lint / tests / build (when frontend/package.json exists)
```

It exits non-zero on the first failed step and prints a timing summary. Use
`-SkipFrontend`, `-SkipDocker` or `-BuildDocker` to trade speed for coverage.
The local baseline on 2026-11-19 passed all nine checks; the Docker image build
and live acceptance still run in GitHub Actions (or manually with
`make docker-smoke`), because they need a running Docker daemon.
