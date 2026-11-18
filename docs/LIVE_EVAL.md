# Live pipeline eval (real provider)

`cmd/live-eval` runs the real **interviewer → analyst → synthesizer** pipeline
(`internal/pipeline`) over a tiny, fixed set of golden cases with the **real
provider models** built by `internal/llm/agents` (`agents.FromEnv` +
`agents.NewPool`). It is the honest counterpart of the offline, deterministic
harness in [AGENT_EVALS.md](AGENT_EVALS.md): the answers are still scripted, but
the questions, extraction and tree synthesis are produced by the live model.

Unlike the offline eval it needs a provider key, network access and paid tokens.
It is a **manual, non-CI check**: `go test ./...` never runs it and does not need
any credentials.

| What | Where |
|---|---|
| Command | [`cmd/live-eval`](../cmd/live-eval/main.go) |
| Live harness | [`internal/agent/eval/live.go`](../internal/agent/eval/live.go), [`live_report.go`](../internal/agent/eval/live_report.go) |
| Pipeline | [`internal/pipeline`](../internal/pipeline/pipeline.go) |
| Provider clients | [`internal/llm/agents`](../internal/llm/agents/pool.go) |
| Offline eval (unchanged) | [`cmd/agent-eval`](../cmd/agent-eval/main.go), [AGENT_EVALS.md](AGENT_EVALS.md) |

## Run

```powershell
# 1. Validate the provider configuration without spending a token.
go run ./cmd/live-eval -dry-run -env-file .secrets\llm.env

# 2. Run both built-in live cases (the default) and save the JSON report.
go run ./cmd/live-eval -env-file .secrets\llm.env -out $env:TEMP\live-eval-report.json

# 3. Run a single case and/or a custom per-case timeout.
go run ./cmd/live-eval -env-file .secrets\llm.env -cases data-analyst-baseline
go run ./cmd/live-eval -env-file .secrets\llm.env -case-timeout 5m
```

`-env-file` is optional. When it is omitted the provider variables must already
be present in the process environment:

```powershell
foreach ($line in Get-Content .secrets\llm.env) {
  if ($line -match '^\s*(LLM_[A-Za-z0-9_]+)\s*=\s*(.*)$') {
    Set-Item "Env:$($Matches[1])" $Matches[2]
  }
}
go run ./cmd/live-eval
```

The loader reads only `LLM_*` variables from the file. The shared local secrets
file also contains database/JWT/OTEL values; live-eval ignores them. An
already-set environment variable wins over the file, so one value can be
overridden for a single run. Values are never echoed.

| Flag | Meaning |
|---|---|
| `-env-file <path>` | Optional dotenv file with `LLM_*` provider variables. Only variables that are not already set are loaded. |
| `-cases <a,b>` | Comma-separated subset of the built-in live case names. Duplicates are collapsed; an unknown name is a usage error. |
| `-case-timeout <duration>` | Hard per-case timeout, default `10m`. Bounds the whole case including every retry, repair and model call. Must be positive. |
| `-out <path>` | Write the complete JSON report (metrics, call trace, provider plan). |
| `-dry-run` | Validate the provider configuration and print the plan without making any model call. |

Exit codes: `0` — every live case passed; `1` — a case failed, the provider could
not be configured, or the report could not be written; `2` — flag misuse.

## Live case set

The set is intentionally limited to two cases (never more than 2–3 per run):

| Case | Covers |
|---|---|
| `data-analyst-baseline` | Interview-only data analyst; real interviewer questions, real analyst extraction and taxonomy linking, deterministic synthesizer. |
| `data-analyst-standard-conflict` | Hybrid case: standard and vacancy source **contents are attached**, so the analyst runs the focused per-source extraction. The provided standard/vacancy nodes share a node id with different titles, so the synthesizer must resolve exactly one title conflict. |

Both cases use the same scripted respondent answers by question level, so the
input is stable while the model output is real. The interview plan has six
questions; a model failure on a question degrades the run to a deterministic
fallback question instead of aborting it.

### What is asserted vs what is recorded

* **Asserted (live PASS):** the pipeline completed before the case timeout; both
  model-facing outputs re-validate against the embedded JSON schemas; the tree
  has exactly one `role` root and valid `role → function → task → action`
  parent/child links; every node has evidence with `source_id`, `ref_id`,
  `quote`; interview evidence quotes are verbatim transcript fragments; every
  conflict references an existing node, has at least two values and the correct
  highest-priority winner. These are the same invariants as the offline harness.
* **Asserted, deterministic only:** the conflict case carries
  `expect.min_conflicts: 1`, because the conflict comes from the provided
  standard/vacancy nodes, not from the model.
* **Recorded, not asserted:** run status (`done` / `degraded`), question and
  clarification counts, fallback questions, entities, tree size and node types,
  evidence count, conflict and message counts, latency, provider attempts,
  model errors, fallback calls, actual model names and the first redacted error.
  A real model may legitimately return a different tree, so these are
  observations for comparison, not pass/fail gates.

## Provider configuration

Measured on 2026-11-19 against the local ignored `.secrets/llm.env` (the API key
is never shown here or by the command):

| Setting | Value |
|---|---|
| Provider base URL | `https://api.reformboss.com/v1` |
| Interviewer model | `deepseek-v4.1-flash-1017` |
| Analyst model | `minimax-m3.1-flash` |
| Analyst fallback model | `deepseek-v4.1-flash-1017` (configured, unused in this run) |
| Synthesizer | deterministic, in-process `internal/agent/synthesizer`, no model call |
| Per-call timeout | `LLM_TIMEOUT_S=300`, interviewer/analyst `300` |
| Per-case timeout | `10m` (command default) |
| Concurrency / retries | pool defaults (`LLM_MAX_CONCURRENCY=3`, `LLM_REQUESTS_PER_MINUTE=20`, `LLM_RETRY_MAX=3`) |

The pool applies the production call stack: guardrails, circuit breaker with the
per-agent timeout, the bounded gate, and the optional primary → fallback model
chain. Fallback usage is measured because the live harness decorates every
per-model client (primary and fallback) with a call recorder inside the pool.

## Measured results

Command used:

```powershell
go run ./cmd/live-eval -env-file .secrets\llm.env -out $env:TEMP\live-eval-report.json
```

Raw command output:

```text
LIVE AGENT PIPELINE EVAL (real provider)
provider: https://api.reformboss.com/v1
  interviewer: model=deepseek-v4.1-flash-1017
  analyst: model=minimax-m3.1-flash, fallback=deepseek-v4.1-flash-1017
  synthesizer: deterministic (internal/agent/synthesizer, no model call)
case timeout: 10m0s
cases: 2

CASE                            STATUS  CONTRACT  Q  CLAR  FALLQ  NODES  TYPES                              EVID  CONF  CALLS  FB  ERR  LATENCY  RESULT
data-analyst-baseline           done    yes       6  0     0      8      action:2,function:2,role:1,task:3  10    0     7      0   0    35.54s   PASS
data-analyst-standard-conflict  done    yes       6  0     0      14     action:2,function:4,role:1,task:7  28    1     9      0   0    67.22s   PASS

MODEL CALLS
  interviewer:
    deepseek-v4.1-flash-1017      12 calls, 0 errors
  analyst:
    minimax-m3.1-flash             4 calls, 0 errors
    deepseek-v4.1-flash-1017       0 calls, 0 errors (fallback)

SUMMARY: 2/2 cases passed | contract valid 2/2 | model calls 16 (errors 0, fallback 0) | total 102.76s
```

Result table:

| Case | Status | JSON contract | Questions | Fallback questions | Entities | Tree nodes | Node types | Evidence | Conflicts | Model calls | Fallback calls | Errors | Latency | Result |
|---|---|---|---:|---:|---:|---:|---|---:|---:|---:|---:|---:|---:|---|
| `data-analyst-baseline` | done | valid | 6 | 0 | 8 | 8 | role 1, function 2, task 3, action 2 | 10 | 0 | 7 | 0 | 0 | 35.54 s | PASS |
| `data-analyst-standard-conflict` | done | valid | 6 | 0 | 13 | 14 | role 1, function 4, task 7, action 2 | 28 | 1 | 9 | 0 | 0 | 67.22 s | PASS |

Provider attempts (from the JSON `call_trace`):

| Agent | Model | Calls | Errors | Role |
|---|---|---:|---:|---|
| interviewer | `deepseek-v4.1-flash-1017` | 12 | 0 | primary |
| analyst | `minimax-m3.1-flash` | 4 | 0 | primary |
| analyst | `deepseek-v4.1-flash-1017` | 0 | 0 | configured fallback, unused |

Observed latency breakdown: interviewer calls were 2.2–4.9 s each; the analyst
call took 19.6 s for the baseline. The conflict case made three analyst calls
27.7 s + 10.0 s + 9.7 s = one main extraction plus two focused
standard/vacancy extractions, which is exactly the hybrid path the case is
meant to exercise. Total pipeline wall time: 102.76 s for both cases.

All 16 provider attempts succeeded on the primary models (no fallback, no
retries, no errors); both outputs validated against the agent JSON schemas and
both trees satisfied the hierarchy/evidence invariants. The conflict case
resolved its one deterministic title conflict (standard source beats vacancy on
the shared function node; source priority `interview > standard > vacancy`). The
live harness did not need a fallback question in either case.

The JSON report records the full provider call trace, per-agent and per-model
call counts, first redacted error and all metrics, so a repeat run can be diffed
without re-reading the console table.

## Limitations

* **Provider variance.** Live output is not deterministic: tree size, evidence
  count, entity count and latency change between runs and between models. The
  harness reports these values and asserts only contract invariants (plus the
  one deterministic conflict count). Do not treat the numbers above as an exact
  baseline that must match on every run.
* **Model quality is not judged.** PASS means "pipeline completed and the JSON /
  tree / evidence contract held". Titles, descriptions and taxonomy quality are
  not semantically scored; a valid but low-quality tree still passes.
* **Cost and time.** One full run is 16 provider attempts and ~1.7 minutes on the
  measured configuration; analyst calls dominate the wall time. The run is
  capped at two cases and a 10-minute per-case timeout, and it is deliberately
  not part of CI. Token usage is not exposed in the live report (per-call latency
  and call counts are); use the provider dashboard for cost accounting.
* **Failure reporting is partial by design.** When the pipeline returns an
  error, no partial `pipeline.Result` exists, so the case report contains the
  redacted error, model attempts/errors and elapsed time, but no tree/question
  metrics. A timeout is reported as `case timeout after <duration>: pipeline: ...`
  and the case is never marked as passed.
* **Degraded is not a failure of the harness.** If the interviewer provider
  fails, the session degrades to deterministic fallback questions and the run
  status becomes `degraded`. The status and `fallback_questions` are recorded; a
  degraded run still passes when all contract invariants hold. A failed analyst
  (after the repair loop and the configured fallback model) is a pipeline error
  and fails the case.
* **Small, Russian-language set.** Two scripted data-analyst cases are a smoke
  signal, not a benchmark: they do not cover other professions, long
  transcripts, document sources, PII or guardrail behaviour. The answers are
  synthetic and contain no personal data.
* **Provider guards still apply.** Retries, the circuit breaker, guardrails and
  the primary → fallback chain are the production ones, so a run measures the
  pipeline together with that stack. Fallback counts are reported so a run that
  silently leaned on the fallback model is visible.

## Security

* The API key comes from the process environment or the local, git-ignored
  `.secrets/llm.env`; it is never printed, logged or written to the JSON report.
  The command redacts the configured key from every recorded error, and the
  report contains only model names, the base URL and redacted errors.
* `-env-file` loads only `LLM_*` variables, so the other secrets in the same
  local file (database URL, JWT secret, OTEL settings) are never imported.
* The command does not start or touch the HTTP server, port `8080`, the
  database or n8n.

## Tests

```powershell
gofmt -l cmd\live-eval internal\agent\eval
go vet ./cmd/live-eval ./internal/agent/eval
go test ./cmd/live-eval ./internal/agent/eval
```

`internal/agent/eval/live_test.go` covers the built-in case validation (including
the truthfulness of node → declared source links), the live runner against the
offline script client (metrics, contract, model-call classification), fallback
usage/error accounting, case timeout, redaction of secrets, the call recorder and
the table/JSON/plan writers. `cmd/live-eval/main_test.go` covers the dotenv
loader (only `LLM_*`, quote handling, env precedence, malformed input), dry-run
output without provider calls, case selection, flag misuse and the fact that the
API key never appears in output. All tests are offline: no provider, network or
database is used.
