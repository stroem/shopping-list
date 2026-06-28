# Local API server, config, pool & health (issue #3) — design

**Status:** approved 2026-06-28
**Issue:** [#3 — Local API server (cmd/api) with chi, pgx pool, config, health](https://github.com/stroem/shopping-list/issues/3)
(milestone *M0 — Foundation & infra*)
**Builds on:** #1 (router/entrypoints), #2 (`internal/db.Migrate`, schema).

## Goal

Turn the placeholder API into a real local-dev server: env config, a pgx pool
sized for serverless reuse, shared HTTP middleware, and a **DB-backed `/healthz`**
— the same router the Lambda wraps. Plus the developer ergonomics to run it
locally: a minimal `cmd/migrate` and a root `justfile` with a local-Postgres
recipe.

## Decisions

- **`DATABASE_URL` is required.** `cmd/api` exits with a clear error if it is
  unset — a running server always has a database. (Unit tests do not need one;
  they use a fake pinger.)
- **No auto-migrate in `cmd/api`.** Migrations are a separate step, run via
  `just migrate` → `cmd/migrate`.
- **One `DATABASE_URL` format everywhere:** standard `postgres://…`. `db.Migrate`
  / `db.MigrateDown` normalize the scheme to `pgx5://` internally (golang-migrate's
  pgx/v5 driver); `pgxpool` consumes `postgres://` directly. Refactor from #2's
  `pgx5://`-only contract.
- **`/healthz` returns JSON** (`200 {"status":"ok"}` / `503 {"status":"unavailable","error":…}`),
  replacing #1's plain-text `ok`.

## Components

### `internal/config`
`Config{ DatabaseURL string; Port string }` and `Load() (Config, error)`:
- `DATABASE_URL` — required; empty ⇒ error.
- `PORT` — optional, default `"8080"`.
Env only; no file parsing.

### `internal/db` (existing — extended)
- `NewPool(ctx, databaseURL) (*pgxpool.Pool, error)` — parses the URL, sets
  serverless-friendly limits (`MaxConns=4`, `MinConns=0`, `MaxConnIdleTime=30s`,
  `MaxConnLifetime=5m`) so Neon can scale to zero, and verifies with a `Ping`.
- `Migrate` / `MigrateDown` — refactored to accept `postgres://…` and normalize to
  `pgx5://` via an unexported `migrateURL(string) string` helper (handles
  `postgres://`, `postgresql://`, and an already-`pgx5://` URL).

### `internal/web` (new — shared HTTP helpers, reused by domain handlers #8+)
- **middleware:** chi `RequestID` + `Recoverer`; `DeviceID` middleware that reads
  the `X-Device-Id` header into the request context; `web.DeviceID(ctx) string`
  accessor.
- **responses:** `web.JSON(w, status, v any)` and `web.Error(w, status, msg string)`
  → `{"error":"<msg>"}`; JSON `NotFound` / `MethodNotAllowed` handlers.

### `internal/router` (changed)
`New(deps Deps) http.Handler`, `Deps{ DB Pinger }`, where
`Pinger interface{ Ping(ctx context.Context) error }` (`*pgxpool.Pool` satisfies
it; tests pass a fake). Wires the `web` middleware stack, JSON 404/405, and a
DB-backed `/healthz` that pings with a 2s timeout.

### `cmd/api` (changed)
`config.Load` (fatal on missing `DATABASE_URL`) → `db.NewPool` → `router.New(Deps{DB: pool})`
→ serve with `ReadHeaderTimeout` and graceful shutdown (SIGINT/SIGTERM) that closes
the pool.

### `cmd/lambda` (changed)
Updated to the new `router.New(Deps)` signature; builds the pool from
`DATABASE_URL` at cold start. (The env itself is wired by the IaC in #4.)

### `cmd/migrate` (new — minimal)
`go run ./cmd/migrate <up|down>` reads `DATABASE_URL` and calls `db.Migrate` /
`db.MigrateDown`. ~15 lines, no new deps. The **full-featured** migrate CLI
(version/force/steps/flags) is a separate follow-up issue.

### `justfile` (new — repo root)
Recipes: `run` (`go run ./cmd/api`), `migrate` / `migrate-down`
(`go run ./cmd/migrate …`), `db` / `db-stop` (start/stop a local Postgres
container — prefers `podman`, falls back to `docker`), `test` (`go test ./...` +
`flutter test`), `backend-test`, `app-test`, `app-run` (`flutter run -d chrome`),
`build`, `vet`, `tidy`. Default recipe lists them.

### `AGENTS.md` (updated)
Build/run/test section notes `just` recipes, that `cmd/api` requires
`DATABASE_URL`, and `just db` for a local Postgres.

## Data flow

```
cmd/api → config.Load → db.NewPool ─┐
                                    ├─► router.New(Deps{DB: pool})
GET /healthz → deps.DB.Ping(2s ctx) ┘ → 200 {"status":"ok"} | 503 {...}
every request → RequestID → Recoverer → DeviceID(ctx) → handler → web.JSON/Error
```

## Error handling

- Missing `DATABASE_URL`, unparseable URL, or unreachable DB at boot → `cmd/api`
  logs and exits non-zero.
- `/healthz` ping failure at runtime → `503` JSON, server stays up.
- Panics in handlers → chi `Recoverer` → `500` (JSON via a recovery responder).
- Unknown route / method → JSON `404` / `405`.

## Testing (green bar = `go test ./...`, docker-optional)

- **router/health** — fake `Pinger`: ping ok → `200 {"status":"ok"}`; ping error →
  `503` with `"error"` set. No real DB.
- **config** — missing `DATABASE_URL` → error; default `PORT`.
- **web** — `X-Device-Id` reaches context via `web.DeviceID`; `web.Error` emits
  `{"error":...}` with the right status and content-type.
- **db.migrateURL** — `postgres://`/`postgresql://`/`pgx5://` inputs normalize
  correctly (pure-function unit test, no DB).
- **db.NewPool** — testcontainers integration: a real pool pings successfully;
  **skips without docker**. (Reuses the #2 round-trip test's container pattern.)

## Out of scope (owned elsewhere)

Domain routes/handlers and `X-Device-Id` enforcement/auth (#8–#12) · the
full-featured migrate CLI (**follow-up issue**) · the real Lambda/API-Gateway/Neon
deployment and its env wiring (#4).
