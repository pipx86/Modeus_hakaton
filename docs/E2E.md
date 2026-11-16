# E2E v0 — offline pipeline

`cmd/e2e` runs the full **interview → analysis → synthesis** pipeline with a
deterministic fake model. It exercises the same packages the HTTP API will use,
but needs no network, no database and no Docker.

## Run

```powershell
# from the repository root
$env:GOCACHE='..\gocache'; $env:GOTMPDIR='..\tmp'; $env:GOTOOLCHAIN='local'
go run ./cmd/e2e -out e2e-output.json
```

Expected output (measured 10.11.2026):

```text
status:       done
duration:     8ms
questions:    6
entities:     4
tree nodes:   5
conflicts:    1
messages:     1
model calls:  7
saved:        e2e-output.json
```

## What is covered

- interviewer: 6 questions across role/function/task/action, one question per
  turn, schema-valid JSON, repair path available;
- analyst: 4 evidence-backed entities (role, function, task, action), verbatim
  quotes validated against the transcript, taxonomy linking;
- synthesizer: deterministic merge, conflict between standard and vacancy
  resolved by priority (interview > standard > vacancy), conflict message
  emitted;
- tree: every node has evidence, hierarchy role → function → task → action;
- export: `e2e-output.json` contains the run, session, transcript, analysis,
  tree, conflicts, messages, duration and model call count.

## What is NOT covered (honest gaps)

This is **e2e v0**, not the full card 21 acceptance:

- no live LLM/provider call (fake client only);
- no HTTP API, no Postgres persistence, no Docker Compose stack;
- no approve/export endpoint; approval is represented by the validated tree and
  the saved JSON;
- no video/screenshot and no bug-filing workflow yet.

The full e2e requires the API handlers, store wiring and a live model; those are
the next integration steps after the DB/HTTP layers are connected.
