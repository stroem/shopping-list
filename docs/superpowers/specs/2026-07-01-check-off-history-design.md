# Check-off and append-only purchase history — design

Issue: [#11](https://github.com/stroem/shopping-list/issues/11) (backend, feature)

## Goal

Let a household member check a list item off. Checking off marks the list item
as bought (sets `checked_at`/`checked_by`), records an append-only
`check_off_events` row (the source for the future M3 stats screen), and bumps the
per-household items master frequency counters — all safe to double-tap and replay.

## Acceptance criteria

- Check off → sets `list_items.checked_at`/`checked_by` and writes one
  `check_off_events` row (household-scoped, carrying `list_item_id`, `user_id`,
  `item_id`, `quantity`, `checked_at`).
- `check_off_events` is append-only (insert-only; never updated or hard-deleted by
  this feature) — the source for stats.
- Idempotent via `Idempotency-Key` (HTTP-level replay, existing middleware) **and**
  `client_event_id` (DB-level dedup): a replay with the same `client_event_id`
  writes no second event and does not double-bump.
- Bumps the item master frequency counters (`purchase_count` +1,
  `last_purchased_at` = now) for the checked-off item's name.
- Cross-household or missing list item → 404 (no existence leak).

## Assumptions

- **Route:** `POST /v1/lists/{listId}/items/{id}/check-off` — follows the existing
  nested `/lists/{listId}/items/{id}` list-item routes; the store scopes by
  `(household, id)` (the `{listId}` is validated as a UUID but not re-checked in the
  store, mirroring `PATCH`/`DELETE` list-item handlers).
- **Response:** `200 OK` with the updated `list_items` row (same JSON shape as
  PUT/PATCH list item), so the app can move it out of the active list. No `201`
  — check-off is not resource creation from the client's REST view.
- **Request body:** optional `{"client_event_id": "<uuid>"}`. When present it is the
  client-generated stable event id used for DB-level idempotency; when absent, each
  call appends a new event (append-only, no dedup).
- **`user_id`/`checked_by`:** taken from the authenticated principal
  (`web.Principal.UserID`), never from the client body.
- **Idempotency layering:** the route already sits inside the auth +
  `Idempotency-Key` middleware group, so HTTP replay is handled by the existing
  middleware. `client_event_id` adds defense-in-depth at the DB (a unique partial
  index), so dedup holds even if a stored idempotent response has aged out.
- **Double-count with Add:** `listitems.Add` already bumps the items master on
  fresh insert (add-to-list proxy); check-off bumps again on purchase per this
  issue's explicit criterion. The append-only `check_off_events` is the real stats
  source (per spec §4), so the counter is a soft denormalization. Reconciling the
  two bumps is out of scope (candidate follow-up).
- **`store_id`:** left NULL — store awareness is issue #22.
- **`client_event_id` scope:** the DB unique index is `(household_id,
  client_event_id)` over live rows, so the same id in two households is allowed.

## Approach

New `internal/checkoffs` package (mirrors `listitems` patterns) with a single
transactional `Store.CheckOff(ctx, householdID, id, userID string, clientEventID
*string) (listitems.ListItem, error)`:

1. Verify the list item is live and household-scoped (capture `item_id`, `name`,
   `quantity`); else `ErrNotFound`.
2. Insert the `check_off_events` row. With a `client_event_id`, use
   `ON CONFLICT (household_id, client_event_id) DO NOTHING RETURNING id`; if no row
   returned it is a replay → return the current list item unchanged, no bump.
3. Set `checked_at = now()`, `checked_by = userID`, `updated_at = now()` on the
   `list_items` row (guarded by household + live scope).
4. Bump the items master by name (same `ON CONFLICT (household_id, lower(name))`
   upsert `listitems.Add` uses).

A `golang-migrate` migration `0006` adds `check_off_events.client_event_id uuid`
plus a unique partial index `(household_id, client_event_id) WHERE client_event_id
IS NOT NULL AND deleted_at IS NULL`. Additive and reversible.

The router handler (`internal/router/checkoffs.go`) depends only on a
`CheckOffStore` interface (parameters are primitives + `listitems.ListItem`), so
the `router` package does not import `checkoffs`; `cmd/api` and `cmd/lambda` wire
the concrete store. The route joins the existing auth + idempotency group.

Rejected alternative: dedicating idempotency solely to the `Idempotency-Key`
middleware. It works for HTTP replays but not for a client that regenerates its key
yet re-sends the same logical event; `client_event_id` closes that gap and the
issue names it explicitly.

## Out of scope

- Undo / un-check (issue #14 covers the app's undo UX).
- The stats screen / aggregation (M3).
- Store-aware check-off (`store_id`) — issue #22.
- Reconciling the Add-time vs check-off-time items-master bump.
