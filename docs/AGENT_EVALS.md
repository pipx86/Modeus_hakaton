# Agent pipeline evals (offline, deterministic)

`cmd/agent-eval` runs the real **interviewer → analyst → synthesizer** pipeline
(`internal/pipeline`) over a small golden set of cases with a scripted, in-process
LLM client. It checks the structural guarantees of the output, prints a metrics
table and can save a machine-readable report.

The harness is fully offline: no provider, network, database or Docker is used,
and every model call is deterministic. It is the regression gate for pipeline
quality; live-provider behaviour is covered separately by the provider smoke
tests and is not part of CI.

| What | Where |
|---|---|
| Command | [`cmd/agent-eval`](../cmd/agent-eval/main.go) |
| Harness package | [`internal/agent/eval`](../internal/agent/eval) |
| Golden fixtures | [`internal/agent/eval/testdata/cases`](../internal/agent/eval/testdata/cases) |
| Contract schemas (used by the pipeline) | [`internal/agent/schema`](../internal/agent/schema) |
| Pipeline | [`internal/pipeline`](../internal/pipeline/pipeline.go) |

## Run

```powershell
# from the repository root; all six embedded cases
go run ./cmd/agent-eval

# write the full JSON report as well
go run ./cmd/agent-eval -out agent-eval-report.json

# use an external case file or directory (same schema as the fixtures)
go run ./cmd/agent-eval -cases .\my-cases\
```

| Flag | Meaning |
|---|---|
| `-cases <path>` | A golden-case JSON file or a directory of `*.json` cases. Default: the embedded golden set. |
| `-out <path>` | Write the complete JSON report (metrics + expectations + failures). |

Exit codes: `0` — every case passed, `1` — a case failed, a fixture is invalid,
or the report could not be written, `2` — flag misuse.

The command prints a table and a summary:

```text
AGENT PIPELINE EVAL (offline scripted LLM)
cases: 6 (internal/agent/eval/testdata/cases)

CASE                            STATUS    Q  CLAR  FALL  NODES  EVID  CONF  CALLS  DURATION  RESULT
backend-hybrid-conflict         done      6  0     0     4      6     1     7      6ms       PASS
backend-sparse-analyst-output   done      6  0     0     4      4     0     7      4ms       PASS
data-analyst-baseline           done      6  0     0     4      4     0     7      4ms       PASS
data-analyst-standard-conflict  done      6  0     0     5      6     1     7      4ms       PASS
devops-happy-path               done      6  0     0     4      4     0     7      4ms       PASS
devops-interviewer-degraded     degraded  6  0     1     4      4     0     7      4ms       PASS

SUMMARY: 6/6 cases passed | avg tree nodes 4.2 | avg evidence 4.7 | total pipeline 26ms
```

A failing case is listed after the table with one line per failed assertion, the
command still completes the remaining cases, and the process exits non-zero.

## Golden case schema

Each file in `testdata/cases/` is one case document. `schema_version` must be
`1`; unknown fields and incomplete fixtures are rejected at load time with a
non-zero exit. Case files are loaded in file-name order and the case `name` must
be unique across the set.

| Field | Type | Required | Meaning |
|---|---|---|---|
| `schema_version` | int | yes | Golden-case document version; currently `1`. |
| `name` | string | yes | Unique case name, shown in the report. |
| `description` | string | no | Human-readable intent of the case. |
| `tags` | string[] | no | Free-form labels such as `hybrid`, `fallback`. |
| `profession` | string | yes | Pipeline profession; also the synthetic root role title fallback. |
| `respondent` | object | no | `domain.Respondent` (`id`, `name`, `role`, `level`, `profession`). |
| `answers` | object | yes | Scripted respondent answers by question level: `role`, `function`, `task`, `action`; optional `default`. Keep answers specific (>4 words, no vague markers) or the session asks a clarification question. |
| `sources` | object[] | no | Declared sources (`id`, `type`, `title`). `interview` sources identify the transcript evidence. |
| `standard` | node[] | no | Standard role-model nodes fed into the synthesizer. |
| `vacancy` | node[] | no | Vacancy role-model nodes fed into the synthesizer. |
| `script.analyst` | object | yes | The fake analyst answer: `{"entities": [...]}` in the `agent.io/v1` analyst output shape. Every entity needs `id`, `type`, `title`, `evidence`; each evidence needs `id`, `source_id`, `ref_id`, `quote`, and the quote must be a verbatim transcript fragment (checked by the analyst validator). |
| `script.interviewer.error_on_calls` | int[] | no | 1-based interviewer call numbers that return a transport error, to exercise fallback/degraded behaviour. |
| `script.interviewer.questions` | object | no | Optional question text override per level (`role`, `function`, `task`, `action`). |
| `expect` | object | yes | Assertions, see below. |

A node uses the domain shape
`{"id", "type", "title", "description", "parent_id", "evidence": [...]}`; every
node must carry evidence with `source_id`, `ref_id` and `quote`.

Example (abridged):

```json
{
  "schema_version": 1,
  "name": "data-analyst-baseline",
  "profession": "Аналитик данных",
  "answers": {
    "role": "Я аналитик данных, отвечаю за отчётность и качество данных в продуктовой команде.",
    "function": "Моя функция — отчётность и визуализация: готовлю регулярные отчёты и дашборды.",
    "task": "Пишу SQL-запросы к хранилищу и строю дашборды для продуктовых команд.",
    "action": "Оптимизирую SQL-запросы и проверяю выгрузки перед публикацией отчёта."
  },
  "script": {
    "analyst": {
      "entities": [
        {
          "id": "role-analyst",
          "type": "role",
          "title": "Аналитик данных",
          "evidence": [{
            "id": "ev-role-1",
            "source_id": "src-interview",
            "ref_id": "answer-1",
            "quote": "Я аналитик данных, отвечаю за отчётность и качество данных в продуктовой команде."
          }]
        }
      ]
    }
  },
  "expect": {
    "status": "done",
    "questions": 6,
    "tree_nodes": 4,
    "node_types": ["role", "function", "task", "action"],
    "min_evidence_per_node": 1,
    "evidence": 4,
    "conflicts": 0,
    "model_calls": 7
  }
}
```

### Expectations

All `expect` fields are optional; when present they are checked exactly. The
harness also always verifies the invariants listed in the next section.

| Field | Type | Meaning |
|---|---|---|
| `status` | string | Expected `run.status` (`done`, `degraded`, ...). |
| `questions` | int | Number of transcript turns. The default interviewer plan has six. |
| `clarifications` | int | Number of clarification questions. |
| `fallbacks` | int | Number of deterministic fallback questions (interviewer failures). |
| `tree_nodes` | int | Number of synthesized tree nodes. |
| `node_types` | string[] | Node types that must appear at least once. |
| `min_evidence_per_node` | int | Minimum evidence records per tree node. |
| `evidence` | int | Total evidence records across the tree. |
| `conflicts` | int | Number of resolved conflicts. |
| `conflict_winners` | string[] | Expected winner source type per conflict, in output order. |
| `conflict_values` | int[] | Expected number of competing values per conflict. |
| `model_calls` | int | Expected scripted model calls (interviewer + analyst). |
| `verbatim_quotes` | bool | Check interview evidence quotes against the transcript. Default: `true`; set `false` only for cases that intentionally normalise quotes. |

## Always-on invariants

Even without expectations, a case fails if any of these contract guarantees
break:

- the analyst and synthesizer outputs re-validate against the embedded JSON
  schemas (`contract_valid` metric);
- the tree has exactly one root, the root is a `role`, and every child/parent
  pair follows `role → function → task → action`;
- tree node IDs are unique and non-empty, titles are non-empty, every node has
  at least one evidence record, and every evidence record has `source_id`,
  `ref_id` and `quote`;
- evidence attributed to the interview source appears verbatim in the
  transcript (whitespace-normalised, same rule the analyst uses);
- transcript, session questions and session answers agree: one question line and
  one answer line per recorded turn, unique question IDs, valid question levels;
- every resolved conflict references an existing tree node, has at least two
  values, a non-empty reason, and its winner is the highest-priority value.

## Metrics

The JSON report and the table expose these metrics per case:

| Metric | Meaning |
|---|---|
| `status` | `run.status` produced by the pipeline. |
| `contract_valid` | Analyst and synthesizer outputs validate against their JSON schemas. Always `true` for a passing case. |
| `questions` / `clarifications` / `fallbacks` | Transcript turn counts and degraded-interview signals. |
| `entities` | Analyst entities after taxonomy linking and dedup. |
| `tree_nodes` / `node_types` | Synthesized tree size and per-type counts. |
| `evidence` | Total evidence records in the tree (interview + standard + vacancy). |
| `conflicts` / `messages` | Resolved conflicts and synthesizer memory messages. |
| `model_calls` / `interviewer_calls` / `analyst_calls` | Scripted call budget. `model_calls` is a deterministic regression signal (currently `7` = six interview questions + one analyst call). |
| `duration_ms` | Pipeline duration. Informational only: it is recorded but never asserted, and it differs per run/machine. |

## Current baseline

Embedded set: 6 cases, all passing. Measured 2026-11-18 with the local Go
toolchain on Windows; `duration_ms` values are illustrative only.

| Case | Covers | Status | Q | Fallbacks | Entities | Nodes | Evidence | Conflicts | Calls |
|---|---|---|---:|---:|---:|---:|---:|---:|---:|
| `data-analyst-baseline` | taxonomy linking, interview-only tree | done | 6 | 0 | 4 | 4 | 4 | 0 | 7 |
| `data-analyst-standard-conflict` | hybrid sources; standard beats vacancy | done | 6 | 0 | 4 | 5 | 6 | 1 (winner: standard, 2 values) | 7 |
| `devops-happy-path` | non-taxonomy entities, synthesizer-built tree | done | 6 | 0 | 4 | 4 | 4 | 0 | 7 |
| `devops-interviewer-degraded` | interviewer failure → fallback question, `degraded` run | degraded | 6 | 1 | 4 | 4 | 4 | 0 | 7 |
| `backend-hybrid-conflict` | three-way title conflict; interview wins | done | 6 | 0 | 4 | 4 | 6 | 1 (winner: interview, 3 values) | 7 |
| `backend-sparse-analyst-output` | sparse analyst output; synthesizer generates missing ancestors and aggregates evidence | done | 6 | 0 | 2 | 4 | 4 | 0 | 7 |

Aggregate: `6/6 cases passed`, average 4.2 tree nodes and 4.7 evidence records
per case, 7 model calls per case, `contract_valid = true` everywhere. The
`degraded` case is expected to pass: fallback behaviour is part of the contract.

## Adding a case

1. Copy an existing file in `internal/agent/eval/testdata/cases/` and give it a
   unique `name`.
2. Use explicit answers; if an answer has fewer than four words or contains a
   vague marker, the interviewer legitimately asks a clarification question.
3. Make every analyst evidence `quote` a verbatim fragment of the matching
   answer and use the `src-interview` source id for interview evidence.
4. Start with only `status`, `questions` and `node_types` expectations, run the
   command, then pin the stable counts (`tree_nodes`, `evidence`, `conflicts`,
   `model_calls`) you want to protect.
5. Run `go run ./cmd/agent-eval` and `go test ./internal/agent/eval ./cmd/agent-eval`.

Expectation mismatches are the point of the harness: if the pipeline legitimately
changes, update the fixtures and the baseline in this document in the same
change.

## Tests

Unit tests cover the harness itself and replay the embedded golden set offline:

```powershell
go test ./internal/agent/eval ./cmd/agent-eval
```

- `internal/agent/eval/eval_test.go` — fixture loading/validation, scripted
  client routing and error injection, golden-set execution, every assertion
  branch (status, counts, hierarchy, evidence, verbatim quotes, conflicts,
  contract, model-call budget), table/JSON rendering;
- `cmd/agent-eval/main_test.go` — default embedded run, `-cases` file/directory,
  failing case exit code, invalid input handling and `-out` file writing.

`go test ./...` therefore needs no provider, network or database.

## Not covered

The fake client measures the **pipeline contract**, not model quality: prompt
quality, real-model JSON validity, latency, token usage, PII/guardrail
behaviour and provider failover are out of scope. Those are covered by the live
provider smoke tests and by the existing unit suites for
`internal/llm` / `internal/prompts`. The harness intentionally does not wrap the
fake client in the guardrails stack, so guardrail regressions cannot mask pipeline
regressions.

Related docs: [E2E.md](E2E.md) (single offline demo run), [agents/AGENT_IO.md](agents/AGENT_IO.md)
(agent I/O contract), [agents/PIPELINE_ARCHITECTURE.md](agents/PIPELINE_ARCHITECTURE.md)
(pipeline architecture), [AGENT_METRICS.md](AGENT_METRICS.md) (runtime metrics).
