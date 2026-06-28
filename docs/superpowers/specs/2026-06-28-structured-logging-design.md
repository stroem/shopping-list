# Structured logging with log/slog — design

Issue #43. Migrate the backend from stdlib `log` to `log/slog`.

## Problem

The backend uses stdlib `log` (`log.Printf` / `log.Fatalf`) at startup, in
graceful shutdown, and in the panic recoverer. Output is unstructured text with
no level control and no request correlation. We want JSON logs in the deployed
(Lambda) environment, a configurable level, and request-scoped logs that can
carry the chi request ID.

## Approach (approved)

- New `internal/logging` package with two testable units:
  - `ParseLevel(string) slog.Level` — case-insensitive map of `LOG_LEVEL`
    (`debug`/`info`/`warn`/`error`); empty/unknown → `info`.
  - `New(io.Writer, level string) *slog.Logger` — JSON handler at the parsed
    level.
- `internal/config`: add `LogLevel string` sourced from `LOG_LEVEL`, default
  `info`. `DATABASE_URL` stays required.
- Both `main.go` entrypoints build the logger early, call
  `slog.SetDefault(logger)`, and replace `log.Printf`/`log.Fatalf` with slog
  (`slog.Error(...)` + `os.Exit(1)` for fatal startup errors, since slog has no
  Fatal).
- The panic `Recoverer` keeps its `func(http.Handler) http.Handler` signature
  (so `router.New` wiring is untouched) and logs via `slog.Default()`, attaching
  the chi request ID from `middleware.GetReqID(r.Context())`.

## Affected files

- `internal/logging/logging.go` (+ `_test.go`) — new.
- `internal/config/config.go` (+ `_test.go`) — `LogLevel` field + default.
- `internal/web/middleware.go` (+ `web_test.go`) — recoverer → slog w/ req ID.
- `cmd/api/main.go`, `cmd/lambda/main.go` — build logger, `SetDefault`, drop
  stdlib `log`.
- `.env.example` — document `LOG_LEVEL`.

## Testable units

- `ParseLevel` table (debug/info/warn/error/empty/garbage/mixed-case).
- `New` emits JSON and respects the level (debug suppressed at info).
- Config defaults `LogLevel` to `info` and reads `LOG_LEVEL`.
- Recoverer still returns JSON 500 and emits a slog record carrying the
  request ID. `main.go` stays thin (not unit-tested).
