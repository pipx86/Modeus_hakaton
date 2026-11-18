# syntax=docker/dockerfile:1.7

# ---- build stage -----------------------------------------------------------
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

# ---- runtime stage ---------------------------------------------------------
# Distroless static base: glibc-free, no shell, non-root by default.
FROM gcr.io/distroless/static-debian12:nonroot

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
