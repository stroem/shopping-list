# CORS support for the Flutter web client — design

Issue #44. The Flutter app has a web target (`flutter run -d chrome`, `flutter
build web`). Browser `fetch`/`XMLHttpRequest` calls cross-origin to the API are
blocked by the browser unless the server returns CORS headers. No CORS
middleware exists today, so the web client cannot talk to the backend.

## Approach

Mount `github.com/go-chi/cors` (the standard companion to the chi stack already
in use) as the **first** middleware in the shared `router.New`, so both
`cmd/api` and `cmd/lambda` inherit it and preflight `OPTIONS` is answered before
routing (never falling through to chi's 405 MethodNotAllowed handler).

## Decisions

- **Default origins (when `CORS_ALLOWED_ORIGINS` is unset/empty):** allow
  `http://localhost:*` and `http://127.0.0.1:*` (any port, via go-chi wildcard).
  This keeps local dev (`flutter run -d chrome`, which serves on a random
  localhost port) working out of the box **without** defaulting to an
  unconditional `*`. Operators MUST set `CORS_ALLOWED_ORIGINS` to their web
  origin(s) in production. The default is local-only, so it is safe to ship.
- **AllowCredentials: false.** Auth is the device header (`X-Device-Id`) / join
  code, not cookies, so credentialed requests are not needed. `false` is the
  simplest safe choice and avoids the `*`/credentials incompatibility.
- **Methods:** GET, POST, PUT, PATCH, DELETE, OPTIONS.
- **Headers:** `Accept`, `Content-Type`, `X-Device-Id`, `Idempotency-Key`
  (proactive per AGENTS.md sync), `Authorization` (proactive for future auth).
- **MaxAge: 300s** to cut preflight chatter.

## Config

`config.Config` gains `CORSAllowedOrigins []string`, parsed from
`CORS_ALLOWED_ORIGINS` (comma-separated; spaces trimmed; empties dropped). Empty
slice ⇒ documented local-dev default resolved inside the router.

## Affected files

- `backend/internal/config/config.go` — parse `CORS_ALLOWED_ORIGINS`.
- `backend/internal/router/router.go` — `Deps.CORSAllowedOrigins`, mount
  `cors.Handler` first; resolve default when empty.
- `backend/cmd/api/main.go` — pass `cfg.CORSAllowedOrigins` into `Deps`.
- `backend/cmd/lambda/main.go` — read `CORS_ALLOWED_ORIGINS` env directly
  (lambda skips `config.Load`) and pass into `Deps`.
- `backend/internal/router/cors_test.go` — new tests.
- `.env.example` — document `CORS_ALLOWED_ORIGINS`.
- `backend/go.mod` / `go.sum` — add `github.com/go-chi/cors`.

## Test strategy

In package `router` tests, build the router with a known allowed origin via
`Deps.CORSAllowedOrigins`:
1. A cross-origin **GET** with an allowed `Origin` returns
   `Access-Control-Allow-Origin`.
2. A **preflight OPTIONS** (`Origin` + `Access-Control-Request-Method`) returns
   2xx (NOT 405) with `Access-Control-Allow-Methods` and `-Headers` reflecting
   our config (incl. `X-Device-Id`).
3. The default (empty Deps) allows a `http://localhost:NNNN` origin.
DB-backed tests still skip without `DATABASE_URL` (CORS tests need no DB).
</content>
</invoke>
