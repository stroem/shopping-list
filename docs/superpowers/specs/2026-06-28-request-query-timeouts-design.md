# Request & query timeouts — design

**Issue:** #42 "Add per-request and DB query timeouts"

## Problem

Handlers pass `r.Context()` straight to Postgres with no deadline. On a slow or
cold Neon, or a slow trigram query, a single request can hold one of the pool's
`MaxConns: 4` connections indefinitely, starving every other caller. There is no
upper bound on how long a request may run.

## Approach

Add a **request-timeout middleware** wired once in `router.New`, so it covers all
handlers (including `/v1/suggest`). It bounds the request context with
`context.WithTimeout`; pgx observes context cancellation automatically, so the
in-flight query aborts and its connection returns to the pool — no query rewrite
needed (the suggest path already threads `r.Context()` through the service).

## The JSON-504 decision

chi's `middleware.Timeout` only cancels the context and writes a **plain-text**
504 via `w.WriteHeader` — not our `{"error":...}` JSON shape. So we implement a
custom `web.Timeout(d)` modelled on `net/http`'s `TimeoutHandler`:

- The handler runs in a goroutine writing to an in-memory `timeoutWriter` guarded
  by a `sync.Mutex`.
- A `select` races "handler done" vs `ctx.Done()`.
  - **done:** flush the buffered status/headers/body to the real writer.
  - **deadline:** set `timedOut` under the lock and write `web.Error(w, 504,
    "request timed out")` (our JSON shape).
- **Written exactly once:** after `timedOut` is set, a late handler's
  `Write`/`WriteHeader` become no-ops (`Write` returns `http.ErrHandlerTimeout`),
  so the 504 is never followed by a second body. The race detector is clean.
- A panic in the handler is forwarded over a channel and re-panicked on the
  serving goroutine so the outer `web.Recoverer` still produces a JSON 500.

Middleware order: `RequestID → Recoverer → Timeout → DeviceID → routes`.

## Config

`REQUEST_TIMEOUT` (Go duration string, default `5s`) → `Config.RequestTimeout`,
parsed with `time.ParseDuration`, falling back to the default on empty/invalid so
a typo can't disable the deadline. Threaded through `router.Deps.RequestTimeout`;
a non-positive value falls back to `web.DefaultRequestTimeout` (5s), keeping
zero-value `Deps{}` callers/tests working. `healthz` keeps its own inner 2s ping
timeout; the request timeout is the outer upper bound.

## Affected files

- `internal/web/timeout.go` — new `Timeout` middleware + `timeoutWriter`.
- `internal/config/config.go` — `RequestTimeout` field + `REQUEST_TIMEOUT` parse.
- `internal/router/router.go` — `Deps.RequestTimeout`, wire `web.Timeout`.
- `cmd/api/main.go` — pass `cfg.RequestTimeout` into `Deps`.
- `.env.example` — document `REQUEST_TIMEOUT`.

## Test strategy

- `web`: blocking handler + 10ms timeout → asserts single JSON 504, correct
  content-type, no late-write leak, and that it does not hang; fast handler passes
  through untouched; context carries a deadline. Run under `-race`.
- `router`: blocking fake `Suggester` + `Deps.RequestTimeout: 10ms` → JSON 504,
  no hang.
- `config`: default 5s, reads `250ms`, invalid value falls back to 5s.

All fast (no real sleeps > ~10ms); DB-backed tests still skip without
`DATABASE_URL`.
