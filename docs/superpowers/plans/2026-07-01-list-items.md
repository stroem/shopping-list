# Plan — List items CRUD (#10)

## Goal / Architecture / Constraints

Deliver household-scoped CRUD over `list_items` (rows) with quantity/note/
position, catalog-derived aisle, soft-delete, and an `items`-master bump on add.
Mirror the `internal/lists` store + `internal/router/lists.go` handlers exactly.
Backend-only. Cost invariant unchanged (no always-on compute). Food-only scope.
Build/run/test commands and the green bar are defined in `AGENTS.md`.

Spec: `docs/superpowers/specs/2026-07-01-list-items-design.md`.

## Tasks (TDD order)

1. **[task] listitems store: Add + items-master bump + aisle + idempotent replay**
   — files: `backend/internal/listitems/store.go`,
   `backend/internal/listitems/store_test.go`,
   `backend/migrations/0005_items_household_name_unique.up.sql`,
   `backend/migrations/0005_items_household_name_unique.down.sql`;
   depends-on: none.
   test proves (DB-backed, skips without Postgres): `Add` inserts a live
   `list_items` row scoped to the household with `aisle` resolved from
   `catalog.AisleFor(name)` when none is supplied (explicit body aisle wins),
   `quantity` defaulting to 1; the household `items` master gains/gains-count on
   that name (`purchase_count` +1, `last_purchased_at` set); a replay of the same
   list-item UUID returns `created=false` and does **not** double-bump the master;
   a foreign/soft-deleted list id or foreign `item_id` → `ErrNotFound`.

2. **[task] listitems store: List + Update + SoftDelete + scoping**
   — files: `backend/internal/listitems/store.go` (append),
   `backend/internal/listitems/store_test.go` (append);
   depends-on: 1.
   test proves: `List` returns a household-scoped list's live rows ordered by
   `position` then `created_at`; `Update` changes only the supplied
   quantity/note/position (nil left unchanged) and bumps `updated_at`;
   `SoftDelete` excludes the row from `List` and makes a later `Update` →
   `ErrNotFound`; re-`Add` of a soft-deleted id → `ErrNotFound` (terminal);
   cross-household `List`/`Update`/`SoftDelete` → `ErrNotFound`/empty.

3. **[task] router: list-item handlers + wiring**
   — files: `backend/internal/router/listitems.go`,
   `backend/internal/router/listitems_handler_test.go`,
   `backend/internal/router/router.go` (wire `deps.ListItems`);
   depends-on: 1, 2.
   test proves (fake store, no DB): `PUT /v1/lists/{listId}/items/{id}` → 201 on
   create / 200 on replay, 400 on bad UUID or non-positive quantity, 404 on
   `ErrNotFound`; `GET /v1/lists/{listId}/items` → 200 JSON array (and 404 / empty
   when the caller has no household, matching `lists`); `PATCH` → 200 and 400 on
   empty body, 404 on `ErrNotFound`; `DELETE` → 204 and 404 on `ErrNotFound`;
   household comes from the principal, not the client.

## Affected packages

- `backend/internal/listitems` — new store (Add/List/Update/SoftDelete, ListItem
  type, ErrNotFound).
- `backend/internal/router` — new `listitems.go` handlers + `ListItemStore`
  interface; `router.go` wires them next to `lists` under authenticated `/v1`.
- `backend/migrations` — `0005` partial-unique index on
  `items(household_id, lower(name)) WHERE deleted_at IS NULL` (additive).
- `backend/internal/catalog` — reused read-only (`AisleFor`); unchanged.
- `cmd/api` / `cmd/lambda` — wire the new store into `router.Deps` (small edit,
  folded into task 3 if the entrypoints construct Deps).
