# Recipe schema + migrations — design

Status: autonomous · Date: 2026-07-01 · Issue: #32 · Milestone: M4

Foundation for M4 (Recipe import & cook mode). Epic reference:
[2026-06-28-recipe-import-design.md](./2026-06-28-recipe-import-design.md).

## Goal

Add the v1 recipe tables (`recipes`, `recipe_ingredients`) via a new
golang-migrate migration, following existing schema conventions, so the rest of
M4 (import, CRUD, sync, app) has a schema to build on.

## Acceptance criteria

- A new `up` + `down` migration pair exists and applies cleanly via
  `go run ./cmd/migrate up|down` (proven by the up/down round-trip test).
- `recipes` and `recipe_ingredients` tables exist after `up`, gone after `down`.
- Indexes exist for pull-sync (`recipes(household_id, updated_at)` and
  `recipe_ingredients(recipe_id, updated_at)` for a recipe's own sync cursor) and
  the `recipe_ingredients.recipe_id` FK lookup.
- DB-backed tests skip cleanly without Docker/`DATABASE_URL` (existing pattern via
  testcontainers `t.Skipf`).

## Assumptions

- **Migration number `0006`** — next after `0005_items_household_name_unique`.
- **`recipes` columns** (from the epic spec + issue): `id uuid PK
  gen_random_uuid()` (client-generatable), `household_id uuid NOT NULL FK
  households`, `owner_device_id text NOT NULL`, `visibility text NOT NULL DEFAULT
  'private'` with a CHECK constraint restricting to `('household','private')`,
  `title text NOT NULL`, `source_url text`, `source_type text` with a CHECK
  restricting to `('website','youtube','tiktok')`, `image_url text` (nullable),
  `servings int` (nullable), `steps jsonb NOT NULL DEFAULT '[]'`, plus
  `created_at`/`updated_at`/`deleted_at`. Rationale: mirrors existing tables
  (`items`, `lists`); CHECK constraints match the enumerations the epic locked;
  `steps` as `jsonb` matches `food_catalog.raw`/`ean_mappings.raw` jsonb precedent
  and is more flexible than `text[]` (the epic allows either). `source_type` is
  nullable (not every future source is classified up front) but `visibility`
  defaults to the locked `private`.
- **`recipe_ingredients` columns**: `id uuid PK gen_random_uuid()`, `recipe_id
  uuid NOT NULL FK recipes`, `position int NOT NULL DEFAULT 0`, `raw_text text`,
  `name text NOT NULL`, `amount` numeric (nullable), `unit text` (nullable),
  `catalog_id uuid` (nullable FK → food_catalog), `aisle int` (nullable), plus
  `created_at`/`updated_at`/`deleted_at`. Rationale: `amount` as `numeric` allows
  fractional quantities (0.5 l); `position` mirrors `list_items.position`.
  `recipe_ingredients` is recipe-scoped (no direct `household_id`) — it reaches
  the household through its `recipe_id`, mirroring how `store_aisles`/`store_items`
  are keyed by their parent; its sync index is `(recipe_id, updated_at)`.
- **`created_at` on `recipe_ingredients`** — included for consistency with every
  other table even though the epic bullet lists only `updated_at`/`deleted_at`;
  harmless and matches convention.
- **Down migration** drops both tables in FK-safe order
  (`recipe_ingredients` before `recipes`); indexes drop with their tables.
- **No seed/data changes, no Go domain package, no API** — schema only. Those are
  issues #33–#37.

## Approach

Add `backend/migrations/0006_recipes.up.sql` and `0006_recipes.down.sql`
following the `0001_init` style (English identifiers, `timestamptz` audit columns,
FK-ordered drops). Extend the two existing migration tests that pin the schema:
`internal/db/migrate_test.go` (`wantTables` list, up/down round-trip) and
`internal/db/migrate_runner_test.go` (`TestVersionAfterUp` expects the latest
version, currently `5` → `6`). Add an index-presence assertion for the sync + FK
indexes.

## Out of scope

- Recipe import endpoint, Claude extraction, ingredient→catalog matching (#33,
  #34), recipe CRUD + sync wiring (#35), app UI (#36, #37).
- Servings scaling, ratings, cross-household sharing (epic YAGNI list).
- Any Go repository/store code for recipes — schema only.
