# Pull-sync endpoint + idempotency middleware — design

**Issue:** [#12](https://github.com/stroem/shopping-list/issues/12) — Pull-sync endpoints with since-cursor and idempotency
**Milestone:** M2 — Core domain API
**Date:** 2026-06-30
**Status:** approved

## Problem

The server side of offline-first: the Flutter app (M3) reads locally and replays
queued writes; it pulls server changes since a cursor and must apply them safely.
This issue delivers the **pull-sync read endpoint** and the **idempotency
mechanism** that write endpoints use so double-taps and outbox replays never
duplicate. (MVP design spec §5.)

## Context & constraints

- The schema (`0001_init`) already gives every household-scoped table `updated_at`
  + `deleted_at` and a `*_sync` index on `(household_id, updated_at)` — built for
  this. The household-scoped, sync-indexed tables are: `lists`, `items`,
  `list_items`, `check_off_events`, `users`, `stores`, `store_aisles`,
  `store_items`.
- Auth is OIDC/Bearer (`auth.Middleware` → `web.Principal{UserID, HouseholdID}`,
  `auth.RequireAuth`). Sync is scoped to the authenticated principal's household.
- Most entities have tables but **no Go models yet** (owned by #9/#10/#11 and a
  future stores issue). The sync design must not couple to those models or collide
  with that in-flight work.
- `Idempotency-Key` is already an allowed CORS header (`router.go`); no middleware
  exists yet.

## Part 1 — Pull-sync read endpoint

### Registry (decoupled from domain models)

A registry lists the syncable household-scoped tables, each as a projection — no
per-entity Go structs:

```go
// internal/sync
type entity struct {
	name    string   // JSON key, e.g. "list_items"
	table   string   // SQL table
	columns []string // projected columns (includes id, updated_at, deleted_at)
}

var registry = []entity{
	{"lists", "lists", []string{...}},
	{"items", "items", []string{...}},
	{"list_items", "list_items", []string{...}},
	{"check_off_events", "check_off_events", []string{...}},
	{"users", "users", []string{...}},
	{"stores", "stores", []string{...}},
	{"store_aisles", "store_aisles", []string{...}},
	{"store_items", "store_items", []string{...}},
}
```

Column lists are taken from `0001_init`. Every projection includes `id`,
`updated_at`, and `deleted_at`. New tables register here as #9/#10/#11 land.

### Query

```go
// Changes returns, per entity, the household's rows changed since `since`
// (updated_at > since), including soft-deleted rows, plus the next cursor.
func Changes(ctx context.Context, q Querier, householdID uuid.UUID, since time.Time) (Result, error)

type Result struct {
	Cursor  time.Time
	Changes map[string][]map[string]any
}
```

- Runs inside **one `REPEATABLE READ` transaction** so all entities are read from a
  single consistent snapshot.
- Per entity: `SELECT <columns> FROM <table> WHERE household_id=$1 AND updated_at > $2 ORDER BY updated_at` (uses the `*_sync` index).
- **Soft-deletes are included** (a deleted row has `deleted_at` set but still a
  fresh `updated_at`); the client removes them. `deleted_at` is in the projection.
- Rows are returned as `map[string]any` (generic JSON objects) from the projected
  columns — no domain structs.
- **Cursor** = the maximum `updated_at` across all returned rows; when nothing
  changed, the cursor is the unchanged `since`. Monotonic and non-decreasing.

### Handler

`router/sync.go`: `GET /v1/sync` behind `auth.RequireAuth`.

- Household from `web.PrincipalFrom(ctx).HouseholdID`.
- `since` query param parsed as **RFC3339Nano**; absent/empty → zero time (full
  sync). A malformed `since` → `400`.
- Response `200`:
  ```json
  { "cursor": "2026-06-30T10:00:00.123456Z", "changes": { "lists": [ … ], "list_items": [ … ] } }
  ```
  Entities with no changes still appear with an empty array (stable shape).
- Cross-household isolation is inherent (every query filters `household_id`); no
  other household's rows are ever returned.

### Cursor stability

The `updated_at` cursor with a single snapshot read + idempotent client apply is
the chosen model (per the spec's "updated_at cursor"). Clients store the returned
cursor and re-pull with `since = cursor`; because the query is `> since` and the
client applies by `id`, the only residual effect is that a row committed with an
`updated_at` at-or-below an already-returned cursor (a commit-after-snapshot race)
is re-delivered on a later pull when its `updated_at` advances, or caught by the
client's inclusive re-pull. RFC3339**Nano** preserves Postgres microsecond
precision so boundary rows are neither lost nor perpetually re-sent. Pagination
(a row limit) is **out of scope** for v1 — documented; a `limit`+continuation can
be added later without changing the cursor contract.

## Part 2 — Idempotency middleware

### Storage (migration `0003_idempotency_keys`)

```sql
CREATE TABLE idempotency_keys (
    household_id  uuid NOT NULL REFERENCES households(id),
    key           text NOT NULL,
    method        text NOT NULL,
    path          text NOT NULL,
    status_code   int  NOT NULL,
    response_body bytea NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (household_id, key)
);
```

(`.down.sql` drops the table.) `response_body` is the raw captured bytes.

### Middleware (`internal/idempotency`)

```go
func Middleware(store Store) func(http.Handler) http.Handler
type Store interface {
	Lookup(ctx, householdID uuid.UUID, key string) (saved *Response, found bool, err error)
	Save(ctx, householdID uuid.UUID, key, method, path string, status int, body []byte) error
}
```

- Applies only to **mutating methods** (`POST`, `PUT`, `PATCH`, `DELETE`) that
  carry an `Idempotency-Key` header **and** an authenticated principal (household).
  Requests without the header or principal pass straight through.
- On a request: `Lookup(household, key)`. If **found**, write the stored
  status+body and return — the handler never runs (replay = no-op). If **not
  found**, run the handler through a **buffering `ResponseWriter`** that captures
  status + body, then `Save(...)` and flush the captured response to the client.
- Only **success responses are persisted** (status `< 500`); a `5xx` is not stored
  so the client can retry. (4xx is a deterministic client error — stored, so a
  replay is consistent.)
- Concurrency: `Save` uses `INSERT … ON CONFLICT (household_id, key) DO NOTHING`;
  a race where two identical requests run concurrently may both execute once, but
  the stored response is stable thereafter — acceptable for v1 (documented).

### Adoption

Applied now to the existing household write routes (create/join). #9/#10/#11 wrap
their write routes with the same middleware. Key **retention/cleanup** (old keys)
is a documented future ticket.

## Files touched

- `backend/migrations/0003_idempotency_keys.up.sql` / `.down.sql` — new.
- `backend/internal/sync/registry.go` — syncable table registry.
- `backend/internal/sync/sync.go` — `Changes` + `Result` + `Querier`.
- `backend/internal/sync/sync_test.go` — DB-backed sync tests.
- `backend/internal/idempotency/store.go` — pgx-backed `Store` + `Response`.
- `backend/internal/idempotency/middleware.go` — middleware + buffering writer.
- `backend/internal/idempotency/middleware_test.go` — middleware unit tests (fake store).
- `backend/internal/idempotency/store_test.go` — DB-backed store round-trip.
- `backend/internal/router/sync.go` — `GET /v1/sync` handler.
- `backend/internal/router/sync_handler_test.go` — handler test.
- `backend/internal/router/router.go` — register the route + wire idempotency onto write routes.
- `AGENTS.md` — note `/v1/sync` + the idempotency middleware (proposed).

## Testing

Green bar = `go test ./...`; DB-backed tests use testcontainers and **skip cleanly
without Docker**.

- **`sync.Changes` (DB):** seed two households; rows changed after `since` are
  returned (incl. a soft-deleted row); rows at/before `since` are excluded; another
  household's rows never appear; cursor = max `updated_at`; a re-pull with the
  returned cursor yields no rows (stable); full sync (zero `since`) returns all.
- **`/v1/sync` handler:** `401` without auth; `400` on malformed `since`; `200`
  shape with `cursor` + per-entity arrays; household scoping via the principal.
- **idempotency middleware (unit, fake store):** non-mutating method passes
  through; mutating with no key passes through; first mutating call runs handler +
  `Save`; replay with same key returns stored status+body and does **not** invoke
  the handler; different key runs again; `5xx` is not stored.
- **idempotency store (DB):** save then lookup round-trips status+body; `(household,
  key)` uniqueness; lookup miss returns `found=false`.
- **buffering writer (unit):** captures status (default `200`) and body, forwards
  headers.
