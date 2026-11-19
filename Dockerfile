# syntax=docker/dockerfile:1.7

# ============================================================================
# Build stages
# ============================================================================

# ---- build: demo API + local-token (default image) -------------------------
# Pin the same Go toolchain as go.mod (go 1.26.10).
FROM golang:1.26.10-bookworm AS build

WORKDIR /src

# Module download is cached separately from the sources.
COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY prompts ./prompts
COPY api-docs ./api-docs

# Static, stripped binaries: no libc, no shell, reproducible paths.
# /api is the demo entrypoint (in-memory store + chat fallback, /health and
# /metrics); /local-token issues a JWT for the protected /api/v1 endpoints.
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64
RUN go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
 && go build -trimpath -ldflags="-s -w" -o /out/local-token ./cmd/local-token

# ---- server-build: full PostgreSQL backend (cmd/server) --------------------
# Reuses the dependency/module layers of the `build` stage above. Only the
# `server` (and `migrate`) image targets depend on this stage, so the default
# demo image build stays as fast as before.
FROM build AS server-build

# Distroless has no shell/curl, so the runtime image ships a tiny static HTTP
# probe used by Docker/compose healthchecks.
COPY deploy/docker/healthcheck ./deploy/docker/healthcheck

RUN go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
 && go build -trimpath -ldflags="-s -w" -o /out/healthcheck ./deploy/docker/healthcheck \
 && mkdir -p /out/logs \
 && : > /out/logs/.keep

# ---- goose-build: pinned migration runner ---------------------------------
# Same goose version as CI (`db-checks.yml` / `backend-acceptance.yml`).
# CGO_ENABLED=0 produces a static binary for the distroless migration image.
FROM golang:1.26.10-bookworm AS goose-build

ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOBIN=/out
# Only the PostgreSQL driver is needed (compose Postgres); the build tags drop
# the other goose drivers and keep the migration image small.
RUN go install -trimpath -ldflags="-s -w" \
      -tags "no_mysql no_sqlite3 no_mssql no_azuresql no_clickhouse no_libsql no_vertica no_ydb" \
      github.com/pressly/goose/v3/cmd/goose@v3.29.0

# ============================================================================
# Runtime targets
# ============================================================================

# ---- server: cmd/server + Swagger UI + migrations (distroless) ------------
# Full backend: PostgreSQL store, JWT auth, LLM agents (optional), goose-checked
# schema. api-docs/ is required at runtime (cmd/server serves GET /swagger/
# from ./api-docs); agent prompts are embedded into the binary at build time.
FROM gcr.io/distroless/static-debian12:nonroot AS server

WORKDIR /app

COPY --from=server-build /out/server /app/server
COPY --from=server-build /out/healthcheck /app/healthcheck
# cmd/server writes rotating logs to ./logs (LOG_FILE overrides the path);
# keep the directory writable for the non-root user.
COPY --from=server-build --chown=nonroot:nonroot /out/logs /app/logs
# Swagger UI + OpenAPI spec: GET /swagger/ is served from ./api-docs.
COPY --from=server-build /src/api-docs /app/api-docs
# SQL migrations are shipped for parity with the deployment assets; applying
# them is the job of the dedicated `migrate` image below (goose lives there).
COPY migrations /app/migrations
COPY migrations_optional /app/migrations_optional

USER nonroot:nonroot
EXPOSE 8080

# Distroless has no shell/curl: the static probe GETs /api/v1/health.
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=5 \
  CMD ["/app/healthcheck"]

ENTRYPOINT ["/app/server"]

# ---- migrate: goose + migration files for the compose `migrate` service ----
# `docker compose --profile server up -d server` runs this image first and the
# backend only starts after it exits 0 (goose up is idempotent on a current DB).
FROM gcr.io/distroless/static-debian12:nonroot AS migrate

COPY --from=goose-build /out/goose /goose
COPY migrations /migrations
COPY migrations_optional /migrations_optional

USER nonroot:nonroot

# Compose supplies GOOSE_DRIVER=postgres, GOOSE_DBSTRING=<DATABASE_URL> and
# GOOSE_MIGRATION_DIR=/migrations; the default command applies pending main
# migrations. Optional vector migrations are a separate documented run.
ENTRYPOINT ["/goose"]
CMD ["up"]

# ---- api: demo image (default build target; must stay last) ---------------
# Distroless static base: glibc-free, no shell, non-root by default.
FROM gcr.io/distroless/static-debian12:nonroot AS api

WORKDIR /
COPY --from=build /out/api /api
COPY --from=build /out/local-token /local-token
# Swagger UI is served from ./api-docs by the demo API.
COPY --from=build /src/api-docs /api-docs

USER nonroot:nonroot
EXPOSE 8080

# Distroless has no shell/curl, so the API binary probes itself.
HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
  CMD ["/api", "-healthcheck"]

ENTRYPOINT ["/api"]
