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
| `deploy-config` | `scripts/check_deploy.py` (Dockerfile/compose/Grafana/Prometheus/n8n/docs/secrets), `docker compose config --quiet` for default + profiles, required deliverables |
| `frontend` | runs only if `frontend/package.json` exists (none on this branch → auto-skip with a notice) |
| `docker` | builds the image, asserts **< 100 MB**, runs the container and curls `/health`, `/metrics`, `/api/v1/health`, the self-healthcheck and the JWT chat flow via `/local-token` |

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
container healthcheck and the JWT chat smoke test. In the authoring session the
Docker daemon named-pipe is not accessible from the sandbox, so the image build
and `docker compose up` could not run there; `docker compose config` (client
side) and the native demo smoke test were used instead.

## Limitations

* No real LLM calls: `cmd/e2e` uses a deterministic fake model, and the demo API
  uses the interviewer fallback. Provider keys are never available in CI.
* The `db-integration` job does not replace `backend-acceptance.yml`; it is a
  lightweight smoke check that stays green while migrations are in another
  branch.
* No browser/e2e HTTP test of the static web placeholder; its HTML is validated
  statically by `scripts/check_deploy.py`.
