# Observability: OpenTelemetry tracing

The Go backend records OpenTelemetry spans and can export them to any
OTLP/HTTP collector (Jaeger, Grafana Tempo, OpenTelemetry Collector, …).
Exporting is **opt-in**: with no collector configured, spans stay local and the
service behaves exactly as before.

Metrics and dashboards are documented separately in [`docs/GRAFANA.md`](GRAFANA.md).
The same compose `monitoring` profile runs Jaeger, Prometheus and Grafana, so
traces and metrics can be inspected side by side.

## What is traced

| Item | Value |
| ---- | ----- |
| Tracer name | `activity-descriptor/agents` |
| Span name | `agent.chat` (one span per LLM agent call) |
| Span attributes | `agent`, `model`, `reasoning` |
| Errors | recorded on the span with `status = ERROR` |
| OTLP resource | `service.name = activity-descriptor` |

The span is opened by `internal/llm/agents.TracedClient` through
`observability.Start` and closed with `observability.Finish`. The provider is
installed by `observability.Setup()`, called from `cmd/server` (`cmd/api`, the
compose demo API, does not install it yet).

## Environment variables

| Variable | Default | Meaning |
| -------- | ------- | ------- |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | empty | OTLP/HTTP collector URL, for example `http://localhost:4318`. Empty means **no exporter**: spans are recorded by the SDK and dropped locally. A bare `host:port` is also accepted; the URL form is the standard OTel syntax and is recommended. |
| `OTEL_EXPORTER_OTLP_INSECURE` | `false` | `true` sends spans over plain HTTP. A URL endpoint already implies its scheme (`http://` → insecure, `https://` → TLS); the flag is what turns a bare `host:port` endpoint into plain HTTP. |

Both variables are read once, in `observability.Setup()`; a change requires a
restart. The backend itself reads only these two. The OTLP/HTTP exporter also
honors the standard `OTEL_EXPORTER_OTLP_HEADERS`, `OTEL_EXPORTER_OTLP_TIMEOUT`
and `OTEL_EXPORTER_OTLP_COMPRESSION` variables, which is useful for hosted
collectors that require an authentication header; keep header values out of the
repository.

## Startup and shutdown behaviour

* **No endpoint** – the provider is installed and spans are recorded, but no
  exporter is attached. There is no network I/O at startup or shutdown.
* **Endpoint set** – an `otlptracehttp` exporter with a batched span processor
  is attached. Creating the client is local, so startup never waits for the
  collector; spans are flushed in the background. A down collector cannot fail
  or slow down API requests.
* **Invalid endpoint** (bad scheme, missing host, whitespace) – `Setup` returns
  a clear error, the global provider is left untouched and `cmd/server` logs
  `tracing_setup_failed` and keeps serving. Fix the variable to enable tracing.
* **Shutdown** – the returned function flushes the pending batch (each export
  attempt is bounded to 5 s) and is safe to call more than once. It is deferred
  in `cmd/server` after the HTTP server stops.

Log lines emitted at startup:

| Message | Meaning |
| ------- | ------- |
| `tracing_export_disabled` | no endpoint configured, tracing is local-only |
| `tracing_export_enabled` | exporter attached, endpoint logged |
| `tracing_setup_failed` | endpoint misconfigured; service continues without tracing |

## Run a collector

### Optional compose service (monitoring profile)

`docker-compose.yml` includes Jaeger under the existing `monitoring` profile:

```bash
docker compose --profile monitoring up -d jaeger
# UI:        http://localhost:16686
# OTLP/HTTP: http://localhost:4318
```

The `api` service receives `OTEL_EXPORTER_OTLP_ENDPOINT` /
`OTEL_EXPORTER_OTLP_INSECURE` from the shell environment. Note that compose runs
the demo `cmd/api`, which does not call `observability.Setup()`; the production
`cmd/server` is the traced entrypoint. From inside the compose network the
collector URL is `http://jaeger:4318`.

Prometheus and Grafana are started by the same profile, for example
`docker compose --profile monitoring up -d --no-deps prometheus grafana jaeger`
(see [`docs/GRAFANA.md`](GRAFANA.md) for the target and dashboard details).

### Plain Docker

```bash
docker run --rm -d --name activity-jaeger \
  -p 16686:16686 -p 4318:4318 \
  jaegertracing/all-in-one:1.60.0
```

Then run the backend against it:

```bash
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 go run ./cmd/server
```

The `http://` scheme already selects plain HTTP, so `OTEL_EXPORTER_OTLP_INSECURE`
is only needed when the endpoint is written as a bare `host:port`.

Any endpoint works: point `OTEL_EXPORTER_OTLP_ENDPOINT` at a Grafana Tempo,
OpenTelemetry Collector or hosted OTLP/HTTP receiver instead of Jaeger.

## Verify spans

1. Open <http://localhost:16686>, pick service `activity-descriptor` and run an
   interview through the LLM-backed backend so `agent.chat` spans are produced.
2. Without the UI:

   ```bash
   curl -s http://localhost:16686/api/services
   # {"data":["jaeger-all-in-one","activity-descriptor"],"total":2,"limit":0,"offset":0,"errors":null}
   ```

3. Unit tests (no collector needed – they use an in-memory span recorder and
   never contact the network):

   ```bash
   go test ./internal/observability
   ```

## Verified in the local demo (2026-11-18)

The compose `monitoring` profile's Jaeger container was running
(`activity-descriptor-jaeger-1`, image `jaegertracing/all-in-one:1.60.0`, UI
<http://localhost:16686>, OTLP/HTTP <http://localhost:4318>) and real spans from
the instrumented backend were present:

```bash
curl -s http://localhost:16686/api/services
# {"data":["jaeger-all-in-one","activity-descriptor"],"total":2,"limit":0,"offset":0,"errors":null}

curl -s "http://localhost:16686/api/traces?service=activity-descriptor&limit=5"
# returns 5 traces; each trace has one agent.chat span with the tags below
```

Each returned trace contained one `agent.chat` span with
`otel.library.name=activity-descriptor/agents` and the documented attributes
`agent`, `model`, `reasoning`, `otel.status_code=OK` — the exporter, collector
and UI pipeline all work end to end. The spans were produced by an earlier run
of the instrumented backend (`cmd/server`); as noted above, the demo `cmd/api`
does not call `observability.Setup()`, so its process emits no spans.

The metrics counterpart of this verification (Prometheus scraping the local
backend, Grafana provisioning and dashboards) is in
[`docs/GRAFANA.md`](GRAFANA.md).

## Troubleshooting

* **No spans in Jaeger** – check the startup log: `tracing_export_disabled`
  means the endpoint variable was not set for the process that handles LLM
  calls. In compose, remember `api` is the untraced demo command.
* **`tracing_setup_failed`** – the endpoint is malformed. Use a URL such as
  `http://localhost:4318`; a bare `host:port` is also accepted, but the URL is
  the standard OTel form.
* **Connection refused / security errors** – the collector is not reachable or
  TLS is expected. For a local plain-HTTP collector set
  `OTEL_EXPORTER_OTLP_INSECURE=true`.
* **Spans are lost on shutdown** – the exporter batch is flushed on shutdown,
  but a hard kill (`SIGKILL`) skips the flush.

## Dependencies

The exporter is `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp`
v1.26.0, aligned with the existing `go.opentelemetry.io/otel`, `otel/sdk` and
`otel/trace` v1.26.0 modules in `go.mod`. It brings the protobuf/grpc
transitive modules required by OTLP and is the only new direct dependency.

## Files

| File | Purpose |
| ---- | ------- |
| `internal/observability/provider.go` | config/env parsing, resource, OTLP exporter and provider construction |
| `internal/observability/tracing.go` | `Setup`, `Start`, `Finish` |
| `internal/observability/tracing_test.go` | tests for env parsing, endpoint validation, provider wiring and shutdown |
| `cmd/server/main.go` | calls `Setup`, logs exporter status, defers shutdown |
| `docker-compose.yml` | optional `jaeger` service under the `monitoring` profile |
