# v1 Postgres schema & migrations (issue #2) — design

**Status:** approved 2026-06-28
**Issue:** [#2 — Define the v1 Postgres schema and migrations](https://github.com/stroem/shopping-list/issues/2)
(milestone *M0 — Foundation & infra*)
**Parent vision:**
[`2026-06-28-handla-food-mvp-design.md`](./2026-06-28-handla-food-mvp-design.md) §4

## Goal

Deliver the v1 relational schema as embedded golang-migrate migrations plus a
reusable `Migrate` runner, verified by an up/down round-trip test against a real
Postgres. **Schema + migrations + runner only** — no Go row structs, repositories,
or queries (those land with each domain ticket #8–#12).

## Decisions

- **Migration tool:** `golang-migrate/migrate/v4`, one versioned pair
  `0001_init.{up,down}.sql` in `backend/migrations/`, **embedded** via `//go:embed`
  so the Lambda artifact carries them (no runtime file-path dependency).
- **Single combined init migration** — greenfield v1 schema; `up` creates
  everything, `down` drops everything atomically.
- **UUID keys:** `id uuid PRIMARY KEY DEFAULT gen_random_uuid()`; the app may still
  supply its own id for idempotent offline writes. (`gen_random_uuid()` is built
  into Postgres 13+.)
- **Soft delete + sync columns on every table:** `created_at`,
  `updated_at timestamptz NOT NULL DEFAULT now()`, `deleted_at timestamptz NULL`.
- **Denormalized `household_id`** on `list_items` and `check_off_events` (not only
  reachable by join) so 404-scoping (#8) and `?since` pull-sync (#12) are a
  join-free, indexed `WHERE household_id = ? AND updated_at > ?`.
- **All identifiers English.** Provenance via `source` + `external_id`. A nullable
  `raw jsonb` on the reference tables stashes the original source record so future
  enrichment (nutrients, allergens) needs no re-import and no breaking migration.
- **Search index:** `pg_trgm` GIN trigram on name columns (fuzzy + prefix
  autocomplete). No tsvector FTS in v1 — ranking is by purchase frequency, not text
  relevance.

## Schema (8 tables)

Shared columns on every table unless noted: `id uuid PK DEFAULT gen_random_uuid()`,
`created_at`, `updated_at` (`NOT NULL DEFAULT now()`), `deleted_at NULL`.

**`households`** — `name text`. (The `id` is the household join code.)

**`users`** — `device_id text NOT NULL UNIQUE`, `display_name text`,
`household_id uuid NULL REFERENCES households(id)`.

**`lists`** — `household_id uuid NOT NULL REFERENCES households(id)`,
`name text NOT NULL`, `archived_at timestamptz NULL`.

**`items`** (per-household product master, drives autocomplete) —
`household_id uuid NOT NULL REFERENCES households(id)`, `name text NOT NULL`,
`aisle int NULL`, `image_url text NULL`, `source text NULL`,
`external_id text NULL`, `purchase_count int NOT NULL DEFAULT 0`,
`last_purchased_at timestamptz NULL`.

**`list_items`** — `household_id uuid NOT NULL REFERENCES households(id)`,
`list_id uuid NOT NULL REFERENCES lists(id)`,
`item_id uuid NULL REFERENCES items(id)`, `name text NOT NULL`,
`quantity int NOT NULL DEFAULT 1`, `note text NULL`, `aisle int NULL`,
`position int NOT NULL DEFAULT 0`, `checked_at timestamptz NULL`,
`checked_by uuid NULL REFERENCES users(id)`.

**`check_off_events`** (append-only history) —
`household_id uuid NOT NULL REFERENCES households(id)`,
`list_item_id uuid NULL REFERENCES list_items(id)`,
`user_id uuid NULL REFERENCES users(id)`,
`item_id uuid NULL REFERENCES items(id)`,
`quantity int NOT NULL DEFAULT 1`,
`checked_at timestamptz NOT NULL DEFAULT now()`.

**`food_catalog`** (global reference; Livsmedelsverket generics) —
`source text NOT NULL`, `external_id text NULL`, `name text NOT NULL`,
`food_group text NULL`, `aisle int NULL`, `image_url text NULL`,
`raw jsonb NULL`. Constraint: `UNIQUE (source, external_id)` (idempotent seed).

**`ean_mappings`** (global reference; Open Food Facts barcodes) —
`ean text PRIMARY KEY` (no uuid id), `name text NOT NULL`, `brand text NULL`,
`aisle int NULL`, `image_url text NULL`, `source text NOT NULL`,
`raw jsonb NULL`, `created_at`, `updated_at`, `deleted_at`.

## Indexes (minimal, purpose-driven)

- `CREATE EXTENSION IF NOT EXISTS pg_trgm;`
- GIN trigram on `items (name gin_trgm_ops)` and `food_catalog (name gin_trgm_ops)` — autocomplete.
- `items (household_id, purchase_count DESC, last_purchased_at DESC)` — ranked autocomplete.
- `(household_id, updated_at)` on `lists`, `items`, `list_items`, `check_off_events`, `users` — pull-sync cursor + scoping.
- `list_items (list_id)` — list rendering.
- `UNIQUE (source, external_id)` on `food_catalog` (also an index).

## Components

- **`backend/migrations/0001_init.up.sql` / `0001_init.down.sql`** — the schema.
- **`backend/migrations/embed.go`** — `package migrations`; `//go:embed *.sql` →
  `var FS embed.FS`. Keeps SQL at the AGENTS.md-mandated `backend/migrations/` path
  while making it embeddable.
- **`backend/internal/db/migrate.go`** — `Migrate(ctx context.Context, databaseURL string) error`
  applies all up migrations using `golang-migrate` with an `iofs` source over
  `migrations.FS` and the `pgx/v5` database driver. Treats
  `migrate.ErrNoChange` as success. Consumed by `cmd/api` (#3).

### Interface produced

```go
// package db
func Migrate(ctx context.Context, databaseURL string) error
```

## Testing (AC4)

`backend/internal/db/migrate_test.go`:

1. Skip when no Postgres is available — if docker can't start a container and
   `DATABASE_URL` is unset, `t.Skip(...)` (matches AGENTS.md "DB-backed tests skip
   cleanly").
2. Start an ephemeral Postgres with **testcontainers-go** (`modules/postgres`).
3. Build a `*migrate.Migrate` over the embedded FS; run **Up**; assert all 8 table
   names exist in `information_schema.tables`.
4. Run **Down**; assert all 8 are gone (the `schema_migrations` bookkeeping table
   may remain — that's expected).

Green bar for this issue: `go test ./...` in `backend/` (the migration test runs
when docker is present, skips cleanly otherwise).

## Out of scope (owned elsewhere)

Go row structs / repositories / queries (per domain ticket #8–#12) · the `cmd/seed`
importer that fills `food_catalog`/`ean_mappings` (#5/#6) · DB-backed health check
and pool wiring in `cmd/api` (#3) · nutrient/allergen modeling (deferred; `raw
jsonb` reserves room).
