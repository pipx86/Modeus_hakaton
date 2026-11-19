# hh.ru integration

## Credentials

The project uses two kinds of hh.ru values:

| Variable | Meaning |
|---|---|
| `HH_CLIENT_ID` | application OAuth client id from dev.hh.ru |
| `HH_CLIENT_SECRET` | application OAuth client secret |
| `HH_ACCESS_TOKEN` | optional existing bearer token; takes precedence |
| `HH_USER_AGENT` | required hh.ru application name + real contact |
| `HH_TOKEN_URL` | optional token endpoint, default `https://hh.ru/oauth/token` |

Real values belong only in the git-ignored `.env` or `.secrets/` directory.
They must not be committed, printed in logs, or put into the database.

## How token selection works

`cmd/ingest` uses `ingestion.HHTokenSource`, which chooses credentials in this order:

1. `HH_ACCESS_TOKEN`, if set (static, never refreshed);
2. otherwise, if `HH_CLIENT_ID` and `HH_CLIENT_SECRET` are set, it exchanges
   them server-to-server for an application token:

```text
POST https://hh.ru/oauth/token
grant_type=client_credentials
client_id=...
client_secret=...
```

3. otherwise, it calls the public vacancy search without a bearer token.

The exchange result is held only in memory and is passed as a Bearer token to
the hh.ru API. It is never logged or persisted.

### Auto-refresh

`HHTokenSource` caches the token and automatically renews it when less than
~60 seconds remain until `expires_in`. If hh.ru answers with
`403 app token refresh too early`, the still-valid cached token is kept and
used; the error is not propagated while the cache is alive.

## Usage

```powershell
$env:HH_USER_AGENT = 'MyApp/1.0 (your-contact@your-domain.ru)'
# HH_CLIENT_ID / HH_CLIENT_SECRET are loaded from local .env or environment
go run ./cmd/ingest vacancies -query 'аналитик данных' -pages 1
```

For local Windows token storage, the existing `scripts/Configure-Hh.ps1` and
`scripts/Import-HhVacancies.ps1` workflow remains supported.

## Security

- Do not send real client secrets in chat, tickets, commits or screenshots.
- Rotate the client secret in dev.hh.ru if it was exposed.
- `hh.ru` requires a meaningful `HH-User-Agent`; use an application name and a
  contact address.
- The application token is short-lived; the Go client re-exchanges it when a
  new process starts, so no token file has to be committed.

## Важный нюанс hh.ru

`client_credentials` выдаёт app-token не без ограничений: если приложение уже
получало токен, повторный запрос может вернуть:

```text
403 {"error":"forbidden","error_description":"app token refresh too early"}
```

В этом случае:

1. возьмите действующий app-token в кабинете https://dev.hh.ru/admin и
   положите его в `HH_ACCESS_TOKEN` локального `.env`; или
2. дождитесь окна refresh и повторите запрос.

Код уже поддерживает оба пути: `HH_ACCESS_TOKEN` имеет приоритет, иначе
выполняется `client_credentials`. Токен не печатается и не сохраняется в Git.
