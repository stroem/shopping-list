# Handler error logging (5xx observability) — design

Issue #41. HTTP handlers map internal errors to generic 5xx bodies but discard
the underlying `err`, so production failures are invisible in logs.

## Problem

- `suggestHandler` returns `500 {"error":"suggest failed"}` without logging the
  real error from `s.Suggest(...)`.
- `healthz` returns 503 on a DB ping failure: it never logs the error *and* it
  leaks the raw `err.Error()` into the client body.

## Approach

Add a shared helper in `internal/web` so every 5xx site logs consistently and
the client body stays generic:

```go
func ServerError(w http.ResponseWriter, r *http.Request, err error, msg string)
```

It logs `slog.ErrorContext(r.Context(), msg, "err", err, "request_id",
middleware.GetReqID(r.Context()))` (matching the panic recoverer), then writes
`web.Error(w, 500, msg)`. `suggestHandler` calls it.

`healthz` keeps its 503 + `{status, error}` body shape but: logs the real error
via `slog.ErrorContext` with the request ID, and replaces the leaked
`err.Error()` with a static generic string ("database unavailable").

## Affected files

- `backend/internal/web/respond.go` — add `ServerError`.
- `backend/internal/router/suggest.go` — use `web.ServerError`.
- `backend/internal/router/health.go` — log error, genericize body.
- Tests: `web_test.go`, `suggest_handler_test.go`, `router_test.go`.

## Test strategy

TDD. Capture slog output by swapping `slog.SetDefault` to a JSON handler over a
`bytes.Buffer`, restored in `t.Cleanup` (the `TestRecovererLogsPanicWithRequestID`
pattern). Drive each handler down its error path and assert the log record has
level ERROR, a non-empty `request_id`, and the underlying error; assert the
client body stays generic (no raw error leaked). `go test ./...`, `go build`,
`go vet` clean.
