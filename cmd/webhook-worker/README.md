# webhook-worker

Delivers the persisted `run.completed` outbox events (`agent_messages`,
`message_type = 'run.completed'`) to an HTTP receiver, currently the n8n
workflow [`run_completed_email.json`](../../deploy/n8n/workflows/run_completed_email.json)
(`POST /webhook/run-completed`).

The PostgreSQL trigger only *enqueues* the event; this separate process signs and
sends it, then marks the row `sent` (or `failed` + retry after
`-retry-delay`). Keep it running next to the backend — nothing in the API or
compose stack starts it.

## Configuration (environment)

| Variable | Required | Meaning |
| -------- | -------- | ------- |
| `DATABASE_URL` | yes | PostgreSQL with the project migrations applied. |
| `WEBHOOK_URL` | yes | Receiver endpoint, `http(s)://` without credentials, e.g. `https://<host>/webhook/run-completed`. |
| `WEBHOOK_SECRET` | yes | HMAC-SHA256 key for the `X-Webhook-Signature` header (lowercase hex of the exact request body bytes). |

The n8n workflow does not verify the HMAC header (it only checks the JSON
payload); the signature is still sent so a stricter receiver can verify it.

## Flags

| Flag | Default | Meaning |
| ---- | ------- | ------- |
| `-once` | `false` | Deliver every currently due event, then exit. Exit code 1 if any delivery failed (the event stays `failed` and is retried on the next run). |
| `-poll-interval` | `2s` | Idle polling interval in the daemon mode. |
| `-retry-delay` | `1m` | Delay before a failed event becomes due again. |

## Run

PowerShell (secrets are read from the git-ignored `.secrets/`, never printed):

```powershell
$env:DATABASE_URL   = (Get-Content .secrets\llm.env | Where-Object { $_ -match '^DATABASE_URL=' }) -replace '^DATABASE_URL=',''
$env:WEBHOOK_URL    = 'https://<tunnel-host>/webhook/run-completed'
$env:WEBHOOK_SECRET = '<shared secret from .secrets/webhook.env>'
go run ./cmd/webhook-worker            # daemon
go run ./cmd/webhook-worker -once      # drain the due backlog and exit
```

Bash:

```bash
set -a; . ./.secrets/llm.env; . ./.secrets/webhook.env; set +a
go run ./cmd/webhook-worker
go run ./cmd/webhook-worker -once
```

The n8n side is documented in
[`docs/N8N.md`](../../docs/N8N.md#run-completed-email-notifications) and
[`deploy/n8n/workflows/README.md`](../../deploy/n8n/workflows/README.md#run_completed_emailjson).

## Delivery semantics

* at-least-once: one row is locked with `FOR UPDATE SKIP LOCKED` while sending,
  so concurrent workers do not duplicate it; a crash between the HTTP response
  and the `sent` update can redeliver;
* the receiver must deduplicate by `run_id` (the n8n workflow keeps a 7-day
  `run_id` map in its static data);
* a non-2xx response or a network error marks the row `failed`, increments
  `attempts` and schedules the next attempt after `-retry-delay`;
* `run.completed` is unique per run (`uq_agent_messages_run_completed`), so a
  run is enqueued at most once.
