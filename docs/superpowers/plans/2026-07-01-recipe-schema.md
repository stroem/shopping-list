# Recipe schema + migrations — plan

Issue: #32 · Spec:
[2026-07-01-recipe-schema-design.md](../specs/2026-07-01-recipe-schema-design.md)

## Goal / Architecture / Constraints

Add migration `0006_recipes` creating `recipes` + `recipe_ingredients` with
sync/FK indexes, round-tripping cleanly. Schema only — no Go domain code, no API,
no seed/data. Convention source: `backend/migrations/0001_init.*`. Cost invariant
and scope boundaries per AGENTS.md; green bar and canonical commands per
AGENTS.md (`go test ./...`); DB-backed tests skip cleanly without Docker.

## Tasks (TDD order)

1. [task] `recipes` + `recipe_ingredients` tables round-trip and bump schema
   version to 6 —
   files: `backend/migrations/0006_recipes.up.sql`,
   `backend/migrations/0006_recipes.down.sql`,
   `backend/internal/db/migrate_test.go`,
   `backend/internal/db/migrate_runner_test.go`;
   depends-on: none;
   test proves: after `up` both new tables are present (extended `wantTables`) and
   `Version` == 6; after `down` all tables (incl. the new ones) are gone.

2. [task] sync + FK indexes exist on the new tables —
   files: `backend/migrations/0006_recipes.up.sql`,
   `backend/migrations/0006_recipes.down.sql`,
   `backend/internal/db/migrate_indexes_test.go` (new);
   depends-on: 1 (same migration files → strictly sequential);
   test proves: after `up`, `pg_indexes` contains the sync indexes
   (`recipes` on `(household_id, updated_at)`, `recipe_ingredients` on
   `(recipe_id, updated_at)`) and the FK-lookup index on
   `recipe_ingredients(recipe_id)`.

Note: tasks 1 and 2 share `0006_recipes.up.sql`/`.down.sql`, so they are **not**
parallelisable — run strictly in order.

## Affected packages

- `backend/migrations` — new `0006_recipes.{up,down}.sql`.
- `backend/internal/db` — extend `migrate_test.go` + `migrate_runner_test.go`,
  add `migrate_indexes_test.go`.
