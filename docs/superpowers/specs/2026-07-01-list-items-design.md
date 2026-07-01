# List items: add / edit / remove with quantity, note, aisle, position

Issue: [#10](https://github.com/stroem/shopping-list/issues/10) — M2 Core domain API.

## Goal

Give a household the CRUD it needs over the **list items** (rows) on a list:
add a row (free text or a catalog/EAN-backed name) with a quantity and an
optional note, edit its quantity/note/position (reorder), and remove it via
soft-delete. Adding a row also teaches autocomplete by bumping the household
`items` master (`purchase_count`, `last_purchased_at`). This is the row-level
sibling of the household-scoped `lists` CRUD shipped in #57.

## Acceptance criteria

- **Add** a list item under a list with a client-supplied UUID (idempotent
  create-or-replace, mirroring `PUT /v1/lists/{id}`): body carries `name`,
  optional `quantity` (default 1), optional `note`, optional `item_id`
  (catalog/EAN-backed item reference) and optional `aisle`.
  → `PUT /v1/lists/{listId}/items/{id}` returns 201 on create, 200 on
  idempotent replay.
- **Aisle** is set from the catalog suggestion when the caller does not supply
  one: `aisle = body.aisle ?? catalog.AisleFor(name)`.
- **Adding** a row bumps the household `items` master: the matching item's
  `purchase_count` is incremented and `last_purchased_at` set to `now()`; a new
  master row is created if the name is new to the household. A pure idempotent
  **replay** of the same list-item UUID does **not** double-bump the master.
- **List** the rows of a list, ordered by `position` then `created_at`, live
  rows only → `GET /v1/lists/{listId}/items`.
- **Edit** `quantity`, `note`, and/or `position` (reorder) →
  `PATCH /v1/lists/{listId}/items/{id}`; a nil field is left unchanged; the call
  bumps `updated_at`.
- **Remove** soft-deletes the row → `DELETE /v1/lists/{listId}/items/{id}`
  returns 204; the row is then excluded from `GET` and from a later `PATCH`
  (→ 404). Soft-delete is terminal (re-`PUT` of a deleted id → 404), matching
  `lists`.
- **Household scoping:** every operation is scoped to the caller's household;
  a foreign list id, a foreign item id, or a caller with no household →
  **404** (no existence leak), matching `lists`.
- Client UUIDs + `Idempotency-Key` (the existing global idempotency middleware
  already wraps these write routes; the create-only master bump is the second
  guard against double counting).

## Assumptions

- **Route shape** — list items are nested under their list:
  `PUT|GET|PATCH|DELETE /v1/lists/{listId}/items[/{id}]`, mirroring the flat
  `lists` routes. Chosen over a flat `/v1/list-items/{id}` because the list is
  the natural access scope and it reads well; both are equivalent given
  household scoping.
- **Add verb** — `PUT` with a client UUID (create-or-replace), not `POST`,
  to match `lists` and the "Client UUIDs + Idempotency-Key" note; this makes
  outbox replay naturally idempotent.
- **Aisle derivation** — reuse the existing pure `catalog.AisleFor(name)`
  keyword matcher (the same taxonomy the suggest endpoint serves) for the
  fallback, rather than a DB lookup, keeping it deterministic and DRY. An
  explicit `aisle` in the body always wins.
- **`items` master keying** — the master is matched per household by
  case-insensitive name. A new partial-unique index
  `items(household_id, lower(name)) WHERE deleted_at IS NULL` (migration 0005,
  additive/non-destructive) lets the bump be a single race-free
  `INSERT … ON CONFLICT`.
- **Master bump on add only** — the issue says *adding* updates the master, so
  the bump happens on a genuine insert of the list item; check-off-driven
  counting (if any) belongs to the check-off feature (#11), out of scope here.
- **`item_id`** is stored as a nullable FK when supplied and validated to the
  household's own items; it does not itself drive aisle in v1 (name matching
  does). Cross-household `item_id` → 404.
- **`quantity`** defaults to 1 and must be `>= 1`; a non-positive quantity → 400.
- **`checked_at` / `checked_by`** are surfaced read-only in the row JSON but are
  not set here — ticking off is #11.

## Approach

New package `internal/listitems` with a `Store` over `*pgxpool.Pool`, mirroring
`internal/lists`:

- `Add(ctx, householdID, listID, id, in) (ListItem, created bool, err)` — in one
  transaction: verify the list is live and household-scoped (else `ErrNotFound`);
  `INSERT … ON CONFLICT (id) DO UPDATE` the `list_items` row (guarded by
  household + `deleted_at IS NULL`, xmax=0 → created); resolve aisle; and **only
  when created** bump the `items` master via `INSERT … ON CONFLICT
  (household_id, lower(name)) WHERE deleted_at IS NULL DO UPDATE SET
  purchase_count = items.purchase_count + 1, last_purchased_at = now()`.
- `List(ctx, householdID, listID) ([]ListItem, error)` — live rows for a
  household-scoped list, ordered by `position, created_at`.
- `Update(ctx, householdID, id, quantity *int, note *string, position *int)
  (ListItem, error)` — `COALESCE`-style partial update, bumps `updated_at`.
- `SoftDelete(ctx, householdID, id) error` — sets `deleted_at`; `ErrNotFound`
  when no live row matched.

Router: `internal/router/listitems.go` adds `ListItemStore` interface +
handlers, wired in `router.go` under the existing authenticated `/v1` group next
to `lists`, guarded by `deps.ListItems != nil`. Migration `0005` adds the
partial-unique index (up) and drops it (down).

## Out of scope

- Checking off / undo, and check-off events (#11).
- Store-aware aisle ordering and drag-and-drop UX (#16, #22).
- Any Flutter app work — this issue is backend-only (`backend` label).
- Realtime push; sync already carries `list_items`/`items` via the existing
  registry (no change needed).
- Alcohol / non-food categories (out of product scope).
