# Metrics (/metrics)

`internal/metrics` is a **stdlib-only** Prometheus text exposition (version
`0.0.4`) — no `prometheus/client_golang` dependency and no new Go modules. The
demo API serves it on `GET /metrics` (`:8080` by default; also proxied through
nginx at `http://localhost:8081/metrics` for convenience).

## Metric families

| Metric | Type | Labels | Help |
| ------ | ---- | ------ | ---- |
| `activity_descriptor_http_requests_total` | counter | `method`, `route`, `status` | HTTP requests processed |
| `activity_descriptor_http_request_duration_seconds` | histogram | `method`, `route` | HTTP latency |
| `activity_descriptor_http_in_flight_requests` | gauge | — | Requests currently being served |
| `activity_descriptor_llm_requests_total` | counter | `agent`, `model`, `status` | LLM requests (ready for agent wiring) |
| `activity_descriptor_llm_request_duration_seconds` | histogram | `agent`, `model` | LLM latency (ready for agent wiring) |
| `activity_descriptor_llm_tokens_total` | counter | `agent`, `model`, `direction` | prompt/completion tokens (ready for agent wiring) |
| `activity_descriptor_agent_steps_total` | counter | `agent`, `step`, `status` | agent steps (ready for agent wiring) |
| `activity_descriptor_runs_total` | counter | `status` | finished runs (ready for agent wiring) |
| `activity_descriptor_run_duration_seconds` | histogram | `status` | run wall-clock time (ready for agent wiring) |

HTTP buckets: `{5ms … 10s}`; LLM/run buckets: `{100ms … 120s}`; `+Inf` always
included. Families with no samples are omitted from the exposition.

**The demo API emits the HTTP families only.** The LLM/agent/run families are
exported by the package (and queried by production dashboards after the
pipeline is wired), but the demo `cmd/api` chat uses the deterministic fallback
and never calls an LLM, so it does not record them. Wire them from the
production `cmd/server`/pipeline once the backend owner adds the calls.

## Labels and cardinality

* `route` is the normalized `net/http` route pattern when a single router
  matches (`/health`, `/metrics`, `/api/v1/health`). For the backend routes
  mounted under the demo wrapper, the outer middleware sees the nested mux's
  fallback pattern, so the path is normalized instead:
  `/api/v1/runs/run-7/messages` → `/api/v1/runs/{id}/messages`.
* Numeric, UUID and `run-<n>` path segments become `{id}`; **unknown segments
  collapse to `unmatched`**, so arbitrary 404 paths cannot explode cardinality.
* **No `run_id` or `request_id` labels.** High-cardinality identifiers belong in
  logs/traces. `test` asserts this for both the package and the demo API.
* `status` is the numeric HTTP status (`200`, `201`, `401`, …).
* Prompts, documents and credentials are never recorded.

## Instrumentation API

```go
m := metrics.New()
handler := m.Middleware(mux)                    // records HTTP only
m.ObserveHTTPRequest(method, route, status, d)  // explicit HTTP sample
m.ObserveLLMRequest(agent, model, status, d)    // agent/LLM call
m.AddTokens(agent, model, input, output)        // token usage
m.ObserveAgentStep(agent, step, status)         // pipeline step
m.ObserveRun(status, duration)                  // finished run
```

`cmd/api` wires `m.Middleware` around the demo router, so every request is
counted and timed, and the in-flight gauge is maintained automatically.

## Example queries

```promql
# request rate
sum(rate(activity_descriptor_http_requests_total[5m])) by (route)

# p95 HTTP latency
histogram_quantile(0.95, sum(rate(activity_descriptor_http_request_duration_seconds_bucket[5m])) by (le, route))

# error ratio
sum(rate(activity_descriptor_http_requests_total{status=~"5.."}[5m]))
  / clamp_min(sum(rate(activity_descriptor_http_requests_total[5m])), 1e-6)

# chat message endpoint traffic
sum(rate(activity_descriptor_http_requests_total{route="/api/v1/runs/{id}/messages"}[5m])) by (method)

# tokens per minute (after agent wiring)
sum(rate(activity_descriptor_llm_tokens_total[5m])) by (agent, direction) * 60
```

## Verification

```bash
go test ./internal/metrics ./cmd/api
make run            # then: curl -s localhost:8080/metrics
make demo-smoke     # health + metrics + JWT chat flow
python scripts/check_deploy.py   # dashboard metric names cross-checked with the package
```

The local smoke test in this session confirmed `200` with
`Content-Type: text/plain; version=0.0.4`, request counters for `/health`,
`/api/v1/health`, `/api/v1/runs` and `/api/v1/runs/{id}/messages`, the
in-flight gauge, and no run identifiers in the exposition.
