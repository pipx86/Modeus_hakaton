# System prompts v1

Versioned, embedded prompts for the three agents of the Modeus Hackathon
activity-descriptor pipeline:

| Agent | Directory | Used by |
|---|---|---|
| interviewer | `prompts/interviewer/v1/` | `internal/agent/interviewer` |
| analyst | `prompts/analyst/v1/` | `internal/agent/analyst` |
| synthesizer | `prompts/synthesizer/v1/` | `internal/agent/synthesizer` (merge rules; prompt kept for optional LLM normalization) |

## Composition

`internal/prompts.Load` composes a prompt document from three layers, in this
order:

1. `_shared/security.md` - prompt-injection and data-handling rules;
2. `_shared/output_rules.md` - JSON-only output rules shared by all agents;
3. `<agent>/<version>/system.md` - role, task and schema reference.

`examples.md` is loaded separately and appended to the system content by the
agent as few-shot examples.

`internal/prompts.Load` returns a SHA-256 fingerprint of the composed prompt.
It is logged as `prompt_version_id` / `prompt_hash` and stored with every LLM
request, so a model answer can always be traced back to the exact prompt that
produced it.

## Versioning rules

- Never edit an already shipped version directory in place: add
  `<agent>/v2/` and point the agent at it.
- A prompt version bump must be reflected in `internal/prompts.DefaultVersion`
  or passed explicitly through the agent option.
- The output JSON schema version is tracked separately in
  `internal/agent/schema/` (contract `agent.io/v1`).

## Adding a prompt

1. Create `<agent>/v2/system.md` and `<agent>/v2/examples.md`.
2. Add the directory to the `//go:embed` directives in `prompts/embed.go`.
3. Run `go test ./internal/prompts/...` - the tests assert that every embedded
   version has both files, a non-empty system prompt and a stable hash.

Do not put secrets or customer data into prompts: all prompts are embedded in
the binary and readable by anyone with the build.
