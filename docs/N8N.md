# n8n automation (automation profile)

n8n runs on the `automation` profile so the default `docker compose up` stays
lightweight.

## Services

| Service | Image | Port | Persistence |
| ------- | ----- | ---- | ----------- |
| `n8n` | `n8nio/n8n:1.63.4` | 5678 | `n8n_data` volume (`/home/node/.n8n`) |
| `n8n-postgres` | `postgres:16-alpine` | internal | `n8n_pgdata` volume |

n8n uses **Postgres** (not SQLite) so workflows survive container restarts.
Both services have healthchecks and n8n waits for a healthy database.

## Start / stop

```bash
cd .worktrees/<branch>
cp .env.example .env                 # set N8N_DB_* / SMTP_* / MAIL_* if desired
docker compose --profile automation up -d n8n n8n-postgres
# or: make up-automation

docker compose logs -f n8n           # logs
make down                            # stop every profile (keeps volumes)
docker compose --profile automation down -v   # stop + delete automation data
```

Open <http://localhost:5678>. On a fresh database n8n shows the **owner setup**
screen — create the owner account there; access is then controlled by n8n's own
user management. The `N8N_BASIC_AUTH_*` variables in `docker-compose.yml` are
**ignored by n8n 1.63.4** (verified: `/rest/settings` answers `200`
unauthenticated and the image contains no basic-auth middleware) — do not rely
on them for access control.

## Configuration

| Variable | Default | Meaning |
| -------- | ------- | ------- |
| `N8N_PORT` | `5678` | host port |
| `N8N_HOST` | `localhost` | host name used in the editor/webhooks |
| `N8N_WEBHOOK_URL` | `http://localhost:5678/` | URL n8n advertises for webhooks (editor display). For a public tunnel set it to the public HTTPS base; the production webhook path is served even if this stays `localhost` (verified) |
| `N8N_BASIC_AUTH_*` | — | **ignored by n8n 1.63.4** (legacy compose vars); use UI owner setup |
| `N8N_DB_NAME` / `N8N_DB_USER` / `N8N_DB_PASSWORD` | `n8n` / `n8n` / `n8n_dev_password` | automation Postgres |
| `INGESTION_URL` | `http://api:8080/api/v1/ingest` | target of the nightly ingestion workflow |
| `TELEGRAM_CHAT_ID` | empty | target chat for the (disabled by default) error alert |
| `SMTP_HOST` / `SMTP_PORT` / `SMTP_USER` / `SMTP_PASS` | empty / `587` | SMTP relay for `notion_email_notifications.json` (pre-flight validation; the transport itself is an n8n *SMTP account* credential). A real STARTTLS + AUTH relay was verified — see Verification |
| `MAIL_FROM` / `MAIL_TO` | empty | sender/recipient evaluated per execution by the email node |

`N8N_SECURE_COOKIE=false` is required for local plain HTTP access only.
`GENERIC_TIMEZONE` is left at the image default.

## Workflow lifecycle

n8n's CLI runs as the instance owner: on a brand-new database either create the
owner in the UI first or bootstrap it on a disposable environment with
`n8n user-management:reset` (verified to create an unclaimed owner; destructive).

n8n 1.63.4 does not accept a single-object workflow export through
`n8n import:workflow --input=<file>` (it expects a JSON array, error
`workflows.map is not a function`), so import the committed single-object
exports in `--separate` mode:

```bash
# import every committed export (or copy one file into a scratch dir first)
docker compose --profile automation exec n8n sh -c \
  'mkdir -p /tmp/n8n-import && cp /workflows/*.json /tmp/n8n-import/ && \
   n8n import:workflow --separate --input=/tmp/n8n-import'

# create the SMTP credential without the UI (plaintext JSON -> encrypted at rest)
docker compose --profile automation exec n8n n8n import:credentials --input=/tmp/smtp-credential.json

# list / activate workflows (activation requires an n8n restart)
docker compose --profile automation exec n8n n8n list:workflow
docker compose --profile automation exec n8n n8n update:workflow --id=<id> --active=true
docker compose --profile automation restart n8n

# export changes back into this repo
docker compose --profile automation exec n8n n8n export:workflow --all --output=/tmp/all.json
```

`make import-n8n FILE=...` still uses `--input=<single file>` and therefore hits
the pinned-image caveat above; use the `--separate` form. Full details,
including the `n8n import:credentials` JSON format, are in
[`deploy/n8n/workflows/README.md`](../deploy/n8n/workflows/README.md).

Exports live in [`deploy/n8n/workflows/`](../deploy/n8n/workflows/README.md)
and are mounted read-only at `/workflows`. n8n does not auto-import, so a fresh
stack starts clean.

## Committed workflow: `nightly_ingestion.json`

Daily **03:00** schedule plus a manual trigger. It POSTs to
`$env.INGESTION_URL` with `retryOnFail` and two retries (5 s apart), routes
failures through an IF node to a **Telegram alert node that is disabled by
default**, and normalizes success counts (`status`, `ingested`, `vacancies`,
`standards`, `finished_at`). Execution history is enabled; the workflow imports
inactive and the ingestion endpoint must be idempotent per source/day. Full
details: [`deploy/n8n/workflows/README.md`](../deploy/n8n/workflows/README.md).

The backend currently exposes no `/api/v1/ingest` HTTP route (`cmd/ingest` is a
CLI), so the compose default `INGESTION_URL` is a **placeholder for the future
ingestion endpoint**. Set it in `.env` to the real ingestion service/URL before
activating the workflow.

## Integration contract

n8n never talks to the LLM directly and never receives provider keys. It calls
the public API inside the compose network (`http://api:8080`):

* ingestion: `POST $INGESTION_URL` (nightly refresh of vacancies/standards)
* runs (planned/available): `POST /api/v1/runs`, `GET /api/v1/runs/{id}`
* operational: `GET /health`, `GET /metrics`

## Security notes

* Change all passwords in `.env`; `.env` is git-ignored and `.env.example`
  contains development placeholders only.
* The n8n encryption key lives in the `n8n_data` volume. For production set
  `N8N_ENCRYPTION_KEY` from a secret manager and back up the volume.
* `N8N_BASIC_AUTH_*` is **not supported by n8n 1.63.4**. A fresh instance has an
  unclaimed owner, so anyone who can reach port 5678 can claim it — do that
  immediately on first start, and never expose 5678 directly to the internet.
* The `notion-notify` webhook ships with `authentication: none`. Add Header Auth
  (or protect it at the reverse proxy) before exposing it publicly; see
  [`deploy/n8n/workflows/README.md`](../deploy/n8n/workflows/README.md).
* No credentials, tokens or chat ids are committed; Telegram credentials are
  created in the n8n UI and the error node ships disabled with a placeholder
  credential id. The SMTP credential is stored encrypted in Postgres; never
  commit credential exports (`n8n export:credentials --decrypted` is plaintext).
* A Cloudflare **quick tunnel** (`cloudflared tunnel --url …`) publishes the
  entire n8n port, editor included, on a random ephemeral hostname — not just
  the webhook. Treat it as a short-lived test tool: create the owner account
  first, add webhook auth, stop the tunnel right after validation, and never
  hand a quick-tunnel URL to a production integration (it changes on restart).
  Verified details and the exact commands are in the Verification section.
* Exposing the webhook publicly is a write path into the integration: with
  `authentication: none` anyone who learns the URL can trigger emails. Use
  Header Auth (or proxy-level auth) and a stable HTTPS hostname; the Notion
  cloud cannot reach `localhost`.

## Verification

### Static (CI-safe, no Docker daemon needed)

```bash
docker compose --profile automation config --quiet
python3 scripts/check_deploy.py
python3 deploy/n8n/workflows/validate_workflows.py
```

### End-to-end with a local SMTP sink (verified)

On 2026-11-18 the full email path was executed against `n8nio/n8n:1.63.4` +
`postgres:16-alpine` from the `automation` profile, with MailHog as the SMTP
sink. The committed workflow was imported **unchanged**; the SMTP credential was
created non-interactively with `n8n import:credentials` using the placeholder
credential id `REPLACE_WITH_SMTP_CREDENTIAL_ID` (disposable test only).

| Check | Result |
| ----- | ------ |
| `n8n import:credentials` (plaintext JSON) | `Successfully imported 1 credential`; Postgres value starts with `U2FsdGVk…` (encrypted, not plaintext) |
| workflow import (`--separate`) + activate + restart | imported unchanged; webhook registered |
| `POST /webhook/notion-notify` | HTTP 200, body `accepted` |
| MailHog `/api/v2/messages` | `total: 1`, expected From/To/Subject and normalized body |
| n8n execution | `status: success`, run data contains `Summary: email sent` and SMTP `250 Ok: queued as …` |
| empty body / SMTP sink down | skip branch and failure branch respectively; both executions still `success` |
| manual trigger (`n8n execute --id=…`) | demo email delivered |
| `n8n --version` | `1.63.4` |

Full copy-paste recipe (MailHog + credential import + webhook + sink check):
[`deploy/n8n/workflows/README.md`](../deploy/n8n/workflows/README.md#end-to-end-test-with-a-local-smtp-sink-verified).

The MailHog test proves the workflow wiring and the credential handoff, but not
TLS/provider authentication or reachability from the internet. Those two gaps
were closed separately with a real relay and a public tunnel.

### End-to-end with a real SMTP relay (Ethereal, verified)

MailHog accepts plaintext SMTP and any AUTH. To prove the real transport, the
same workflow was run against [Ethereal](https://ethereal.email) — a real SMTP
relay that requires STARTTLS and provider authentication on
`smtp.ethereal.email:587` and also exposes IMAP (`imap.ethereal.email:993`) for
checking the mailbox. Ethereal is still a **test sink**: it authenticates and
stores the message but does not deliver it to the recipient's real mailbox, so
it proves the transport, not external deliverability.

Verified on 2026-11-18 against `n8nio/n8n:1.63.4` + `postgres:16-alpine`, with
the committed workflow imported **unchanged**:

| Check | Observed result |
| ----- | --------------- |
| throwaway account via `POST https://api.nodemailer.com/user` | created; SMTP `smtp.ethereal.email:587` + IMAP endpoint; no signup, no user secrets |
| `n8n import:credentials` (real user/password) | `Successfully imported 1 credential.`; Postgres `credentials_entity.data` starts with `U2FsdGVkX1…` (encrypted, not plaintext) |
| workflow import + activate + restart | production webhook registered; exported workflow unmodified |
| `POST /webhook/notion-notify` | HTTP `200`, body `accepted` |
| n8n execution | `status: success`; run data contains `Summary: email sent` and the SMTP `250` response; no `ECONNREFUSED` |
| Ethereal IMAP inbox | `0 → 3` messages; From `n8n@example.test` → To `notifications@example.test`; subject `[Notion] page.updated: …`; body contains the mapped fields and the n8n execution id |
| `n8n --version` | `1.63.4` |

The generated account/password were used only at runtime (shell env + a temp
credential file, never in the repo) and are not reproduced here. Ethereal
accounts and messages are short-lived; the full copy-paste recipe is in
[`deploy/n8n/workflows/README.md`](../deploy/n8n/workflows/README.md#end-to-end-test-with-a-real-smtp-relay-ethereal-verified).

### Gmail relay (verified)

Gmail is the typical real relay for this workflow: `smtp.gmail.com:587`,
`SMTP_SECURE=false` (STARTTLS), `SMTP_USER`/`MAIL_FROM` = the Gmail address,
`MAIL_TO` = recipient and `SMTP_PASS` = a 16-character Google **App Password**
(2-Step Verification must be on). Credential import and the public-webhook path
are the same as above.

Verified on 2026-11-19 with a real Gmail account behind the public HTTPS
tunnel, with the committed workflow imported **unchanged**:

| Check | Observed result |
| ----- | --------------- |
| public `POST /webhook/notion-notify` | HTTP `200`, body `accepted` through the Cloudflare edge |
| n8n execution | `status: success`, path `Webhook → Normalize payload → Config missing? → Send email via SMTP → Summary: email sent` (no skip/error branch) |
| Gmail SMTP response | `250 2.0.0 OK 1795077189 … - gsmtp` (STARTTLS + AUTH accepted) |
| mailbox check (IMAP, Sent Mail + All Mail) | message present: From `opikmaks06@gmail.com` → To `nikolayfortests@gmail.com`, subject `[Notion] page.updated: Gmail final E2E 2026-11-19T08:33:04Z`, `Message-ID: <e06c559a-…@gmail.com>` |
| credential at rest | imported via CLI; Postgres `credentials_entity.data` starts with `U2FsdGVkX1…` (encrypted) |

Earlier attempts with different app passwords failed at AUTH with
`535-5.7.8 Username and Password not accepted … BadCredentials`; a direct
STARTTLS probe outside n8n returned the same 535 while the credential stored in
n8n was byte-identical to `.secrets/smtp.env`, which proved the transport was
fine and the app password itself was the problem. Keep this in mind when 535
appears; no secrets are recorded here.

To route mail through a local Postfix relay instead of talking to Gmail from
n8n directly, see [Local SMTP relay + Cloudflare webhook](#local-smtp-relay--cloudflare-webhook-verified)
below.

When Gmail returns 535 `BadCredentials`:

* confirm the App Password was created while signed in to the **same** account
  and that 2-Step Verification is enabled (turning 2FA off revokes app
  passwords); **Advanced Protection Program** blocks app passwords entirely;
* delete and create a new App Password under *Google Account → Security → App
  passwords*, then update the n8n credential (a CLI re-import upserts by
  credential id);
* test it outside n8n (e.g. `curl --url smtps://smtp.gmail.com:465 --user
  '<user>:<app-password>' …` or another mail client) — a 535 there confirms an
  account/credential problem, not this workflow;
* check *Recent security activity* in the Google account for blocked sign-ins
  from the server's location/IP and for password/2FA changes;
* if Gmail keeps rejecting it, point the credential at another real relay
  (`smtp.yandex.ru:465`, `smtp.mail.ru:465`, a transactional provider, …) — the
  workflow is relay-agnostic.

### Public HTTPS webhook via a quick tunnel (verified)

The production webhook was exposed with a Cloudflare quick tunnel
(`cloudflared tunnel --url http://localhost:5678`) and driven from its **public
HTTPS URL**, not from `127.0.0.1`:

| Check | Observed result |
| ----- | --------------- |
| `cloudflared tunnel --url http://localhost:5678` | quick tunnel created; public base `https://<random>.trycloudflare.com` |
| public `GET /healthz` | HTTP `200`; `Server: cloudflare`, `CF-Ray: …`, valid TLS (`ssl_verify_result=0`) |
| public DNS name | `104.16.x.x` (Cloudflare anycast), not loopback |
| public `POST /webhook/notion-notify` | HTTP `200 accepted` through the Cloudflare edge |
| n8n execution + Ethereal inbox | execution `success`; a new message with the payload values arrived in the Ethereal inbox |
| `N8N_WEBHOOK_URL` left at `http://localhost:5678/` | public POST still `200 accepted` + email sent — the production path is served regardless of the advertised URL; set `N8N_WEBHOOK_URL`/`N8N_HOST` to the public URL only so the editor displays the right URLs |

The tunnel observed for that run was
`https://sophisticated-composition-miller-commands.trycloudflare.com` — ephemeral
and already torn down. Copy-paste procedure, Windows/Linux variants and caveats:
[`deploy/n8n/workflows/README.md`](../deploy/n8n/workflows/README.md#public-https-webhook-with-a-quick-tunnel-verified).

Security caveats for a quick tunnel (read before repeating this):

* the tunnel exposes the **whole n8n instance**, editor included, not just the
  webhook. The stack used for the test was disposable and the tunnel was stopped
  afterwards. Create the owner account first and keep the tunnel short-lived;
  for anything permanent use a named tunnel (Cloudflare account + token) or a
  reverse proxy with a stable hostname;
* the committed webhook has `authentication: none` — anyone with the URL can
  trigger the email. Add Header Auth on the webhook node (Notion's *Send
  webhook* action can send a custom header) or enforce auth at the proxy
  **before** handing the URL to Notion;
* the quick-tunnel hostname changes on every run. A Notion automation that
  keeps a stale URL fails and Notion pauses it, so a stable HTTPS hostname is
  required for production.

### Local SMTP relay + Cloudflare webhook (verified)

Production-shaped path that keeps SMTP local and never exposes it to the
internet:

```
external POST (JSON)
   │ HTTPS
   ▼
Cloudflare quick tunnel  ── webhook only: Cloudflare does NOT proxy SMTP
   │ HTTPS -> localhost:5678
   ▼
n8n 1.63.4 (Docker project `n8ngmail`)
   │ plain SMTP :587, no inbound AUTH (compose network only)
   ▼
local Postfix relay (deploy/n8n/docker-compose.smtp-relay.yml, no host ports)
   │ smtp.gmail.com:587, STARTTLS + SASL AUTH (Gmail App Password)
   ▼
recipient mailbox
```

The relay is defined in a separate compose file so the root
`docker-compose.yml` stays untouched; it joins the same Docker project/network
(`name: n8ngmail`), publishes **no** host ports, accepts plain SMTP only from
the compose network, allows only senders from `ALLOWED_SENDER_DOMAINS`
(default `gmail.com`) and authenticates to Gmail as `SMTP_USER` with
`SMTP_PASS`. Both values are injected from the git-ignored
`.secrets/smtp.env`; never commit them.

```bash
# start / inspect the relay (bash)
set -a; . ./.secrets/smtp.env; set +a
docker compose -f deploy/n8n/docker-compose.smtp-relay.yml up -d postfix
docker compose -f deploy/n8n/docker-compose.smtp-relay.yml logs -f postfix

# Windows PowerShell
Get-Content .secrets\smtp.env | ForEach-Object {
  if ($_ -match '^(?<k>[A-Za-z_][A-Za-z0-9_]*)=(?<v>.*)$') { Set-Item "env:$($Matches.k)" $Matches.v }
}
docker compose -f deploy/n8n/docker-compose.smtp-relay.yml up -d postfix
```

Point n8n at the relay — credential `REPLACE_WITH_SMTP_CREDENTIAL_ID`
(`host: postfix`, `port: 587`, `secure: false`, `disableStartTls: true`, empty
user/password) and recreate the n8n container with:

| Variable | Value |
| -------- | ----- |
| `SMTP_HOST` / `SMTP_PORT` | `postfix` / `587` |
| `SMTP_USER` / `SMTP_PASS` | any non-empty placeholder — the *Normalize payload* pre-flight check requires the six `SMTP_*` names, but the actual transport is the SMTP credential and the local relay has no inbound auth |
| `MAIL_FROM` | the Gmail account (must match the relay auth user / allowed sender domain) |
| `MAIL_TO` | recipient mailbox |
| `N8N_WEBHOOK_URL` | public tunnel URL |

```bash
docker compose -p n8ngmail --profile automation up -d n8n   # recreate with new env
```

Verified on 2026-11-19 with the committed workflow imported unchanged:

| Check | Observed result |
| ----- | --------------- |
| public `POST /webhook/notion-notify` | HTTP `200 accepted` via the Cloudflare edge |
| n8n execution | `id=5`, `status: success`, path `Webhook → Normalize payload → Config missing? → Send email via SMTP → Summary: email sent` |
| n8n SMTP response (local relay) | `250 2.0.0 Ok: queued as 6FCA773A1D` (postfix accepted, no AUTH needed) |
| Postfix log | `6FCA773A1D: to=<opik@sfedu.ru>, relay=smtp.gmail.com[173.194.73.108]:587, delay=1.6, dsn=2.0.0, status=sent (250 2.0.0 OK … - gsmtp)` |
| mailbox check (IMAP) | message present in Sent Mail + All Mail: From `opikmaks06@gmail.com` → To `opik@sfedu.ru`, subject `[Notion] page.updated: Local relay final E2E 2026-11-19T08:45:58Z` |
| exposure | relay publishes **no host ports** (`docker port` empty); it listens on 25/587 only inside the compose network; `postqueue -p` empty after delivery |

Limitations and caveats:

* **Cloudflare is only the webhook transport.** Cloudflare quick tunnel
  terminates HTTPS and forwards the POST to n8n; it does not, and cannot,
  proxy SMTP. The Postfix relay talks to Gmail directly from the Docker host.
* A local machine (Windows/home network) cannot be a public internet-facing
  SMTP server: direct MX delivery needs a public IP with **port 25**, a
  matching **PTR**, and SPF/DKIM — that requires a VPS or an SMTP relay
  provider, not Cloudflare. That is exactly why the relay uses the Gmail
  smarthost: no inbound SMTP exposure, Gmail signs/delivers.
* The relay trusts its Docker network and has no inbound auth/TLS. Never
  publish `25`/`587` on the host, never tunnel them, and add firewall +
  SASL if it must be shared. The app password is visible to `docker inspect`
  on the host — keep host access controlled and restart needs the env export
  again.
* With the relay, n8n's `250` means "Postfix queued it"; final delivery
  confirmation is the Postfix log line with Gmail's `250 … status=sent`.
  Direct-Gmail mode (section above) returns Gmail's `250` synchronously.
* Gmail limits apply (personal account: ~500 recipients/day, `From` must match
  the authenticated account, App Password + 2FA). See the 535 troubleshooting
  in the Gmail section above.
* `MAIL_FROM`/`MAIL_TO` are read from the n8n container environment — edit
  `.secrets/smtp.env` and recreate n8n to apply.
* Avoid `--remove-orphans` on the root compose file while the relay runs: the
  relay is an "orphan" for that file by design and would be removed.

### Notion automation (not created: no workspace/token)

The Notion side was **not** tested: this environment has no Notion workspace,
integration token or paid-plan access, and a Notion automation cannot be created
without them. Nothing here claims otherwise. The exact procedure to point a
Notion automation at the verified public webhook (when access exists):

1. The webhook must answer on a **stable public HTTPS URL** (a quick tunnel is
   acceptable only for a one-off test — see the caveat above).
2. Notion's *Send webhook* is a paid-plan automation action: in a database open
   **Automations → New automation**, choose a trigger (e.g. *Page added* /
   *Page edited*), then **Add action → Send webhook**.
3. Paste `https://<your-host>/webhook/notion-notify` as the URL. If the webhook
   node uses Header Auth, use **Add custom header** with the same header
   name/value as the n8n credential — Notion's webhook action itself has no
   built-in authentication.
4. Select the database properties to send. Notion sends only the selected
   database page **properties** (never page content), supports **POST only**,
   allows at most five webhook actions per automation and offers no payload
   preview (Notion's docs suggest capturing it with a service like
   webhook.site).
5. The flow's *Normalize payload* node expects the flat aliases described above.
   Notion's property payload needs either property names matching those aliases
   (`title`, `url`, …) or a small edit of the `pick(...)` lists / an extra
   mapping node. This mapping could not be verified without a Notion workspace.
6. Save the automation, edit a page in the database, then check n8n
   **Executions** and the recipient mailbox. A failing webhook action makes
   Notion pause the automation.

Still missing / requires a real environment:

* the Notion automation itself, as described above (external delivery to a real
  mailbox is covered by the Gmail verification above);
* the real ingestion endpoint for `nightly_ingestion.json` (the compose default
  is a placeholder — see above).

### Manual checklist for a real deployment

1. `cp .env.example .env`; fill `SMTP_*` / `MAIL_*` (and `INGESTION_URL`,
   `TELEGRAM_CHAT_ID` for the ingestion workflow). Keep `.env` out of git.
2. `docker compose --profile automation up -d n8n n8n-postgres`; open
   <http://localhost:5678> and create the owner account.
3. Create the **SMTP account** credential (UI → Credentials, then *Test*) or
   import it via `n8n import:credentials`; select it in **Send email via SMTP**
   instead of the `REPLACE_WITH_SMTP_CREDENTIAL_ID` placeholder. For a
   no-signup dry run against a real STARTTLS/AUTH relay use the Ethereal recipe
   above; switch the credential to your provider's relay for actual delivery.
4. Import the workflows with `--separate`, activate them, then
   `docker compose --profile automation restart n8n`.
5. Smoke-test the webhook with `curl` (expect `accepted`), confirm the email is
   really delivered (your mailbox, or the Ethereal inbox during the dry run),
   and check the *Executions* list (`sent` / `skipped` / `error`).
6. Add authentication to the webhook (Header Auth on the webhook node or at the
   reverse proxy), **then** expose it over HTTPS — e.g.
   `cloudflared tunnel --url http://localhost:5678` for a temporary test, or a
   stable reverse proxy/named tunnel for real use — and point the Notion
   automation at `https://<host>/webhook/notion-notify` (steps above).

## Run-completed email notifications (verified)

The backend enqueues a `run.completed` outbox event; a separate worker delivers it to n8n, which sends the email through the local Postfix relay.

```
run completed -> agent_messages(run.completed) -> cmd/webhook-worker
  -> POST /webhook/run-completed -> n8n -> local Postfix -> Gmail smarthost -> MAIL_TO
```

Start the worker (secrets only from local files):

```powershell
$env:DATABASE_URL = '<database url>'
$env:WEBHOOK_URL = 'https://<public-host>/webhook/run-completed'
$env:WEBHOOK_SECRET = '<shared secret>'
go run ./cmd/webhook-worker
```

Import/activate `deploy/n8n/workflows/run_completed_email.json` with the same credential id as the other workflow (`REPLACE_WITH_SMTP_CREDENTIAL_ID`).

Verified on 2026-11-19:

| Check | Result |
|---|---|
| outbox worker | `run.completed event delivered`, row `status=sent` |
| n8n execution | `id=10`, `status=success`, branch `Summary: email sent` (not error/skip) |
| local Postfix | accepted `250 Ok: queued`, relay `smtp.gmail.com`, `status=sent` |
| recipient | `opik@sfedu.ru`, subject `[Activity Descriptor] Анализ завершён: Data Analyst` |

The worker is at-least-once; the n8n workflow deduplicates by `run_id` for 7 days. Do not expose `/webhook/run-completed` without auth for production.
