# Plan — Check-off and append-only purchase history (#11)

Spec: `docs/superpowers/specs/2026-07-01-check-off-history-design.md`

## Goal / Architecture / Constraints

New `internal/checkoffs` package + `POST /v1/lists/{listId}/items/{id}/check-off`
route. Check-off is one transaction: mark the list item bought, append a
`check_off_events` row, bump the items master, idempotent via `client_event_id`.
Reuse the `listitems` store patterns (household-scoped 404, `xmax`/upsert,
items-master bump). Cost invariant unchanged (no always-on compute). Food-only,
unchanged. Green bar per AGENTS.md (`go test ./...`; DB tests skip without docker —
docker IS available here so store tests run).

## Tasks (TDD order)

1. [task] checkoffs store + migration — files:
   `backend/internal/checkoffs/store.go`,
   `backend/internal/checkoffs/store_test.go`,
   `backend/migrations/0006_checkoff_client_event.up.sql`,
   `backend/migrations/0006_checkoff_client_event.down.sql`;
   depends-on: none; DB-backed (testcontainers).
   test proves: `Store.CheckOff` sets `checked_at`/`checked_by` on the list item and
   inserts exactly one `check_off_events` row (household, list_item_id, user_id,
   item_id, quantity); bumps the items master (`purchase_count`+1,
   `last_purchased_at`); a replay with the same `client_event_id` inserts no second
   event and does not double-bump; a missing/foreign list item → `ErrNotFound`.

2. [task] router check-off handler + Deps wiring — files:
   `backend/internal/router/checkoffs.go`,
   `backend/internal/router/checkoffs_handler_test.go`,
   `backend/internal/router/router.go`;
   depends-on: none (router does not import checkoffs; interface uses primitives +
   `listitems.ListItem`, fake store); non-DB.
   test proves: `POST /v1/lists/{listId}/items/{id}/check-off` returns 200 with the
   updated list item, forwarding the principal's household + `UserID` and the
   body's `client_event_id` (never the client's household); 404 when no household
   principal or store returns `ErrNotFound`; 400 on a malformed `{listId}`/`{id}`.

3. [task] wire concrete store into cmd entrypoints — files:
   `backend/cmd/api/main.go`, `backend/cmd/lambda/main.go`;
   depends-on: 1,2; test proves: `go build ./...` / `go test ./...` compile with
   `CheckOffs: checkoffs.NewStore(pool)` wired into `router.Deps`.

Tasks 1 and 2 are independent (disjoint files, no import edge) → run in parallel.
Task 3 depends on both → run after.

## Affected packages

- `backend/internal/checkoffs` — new: transactional check-off store.
- `backend/internal/router` — new handler + route + `Deps.CheckOffs` field.
- `backend/migrations` — `0006` adds `check_off_events.client_event_id` + unique
  partial index.
- `backend/cmd/api`, `backend/cmd/lambda` — wire the concrete store.
