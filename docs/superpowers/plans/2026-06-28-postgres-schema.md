# v1 Postgres Schema & Migrations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the v1 relational schema as one embedded golang-migrate migration plus a reusable `db.Migrate` runner, verified by an up→down round-trip test on a real Postgres.

**Architecture:** SQL lives in `backend/migrations/0001_init.{up,down}.sql`, embedded via a `migrations` package (`//go:embed *.sql`). `internal/db.Migrate` applies it using golang-migrate's `iofs` source + `pgx/v5` driver. A testcontainers-go test round-trips up/down and asserts all 11 tables.

**Tech Stack:** PostgreSQL 16 · golang-migrate/migrate/v4 (iofs source, pgx/v5 database driver) · jackc/pgx/v5 · testcontainers-go (modules/postgres) · pg_trgm.

## Global Constraints

- Go module `github.com/stroem/shopping-list/backend`; `go 1.26`.
- Every table: `created_at`, `updated_at timestamptz NOT NULL DEFAULT now()`, `deleted_at timestamptz NULL`. UUID PKs are `uuid PRIMARY KEY DEFAULT gen_random_uuid()` (except `ean_mappings`, keyed by `ean text`).
- **All column identifiers English.** Provenance via `source`/`external_id`; `raw jsonb` reserved for later enrichment.
- `household_id` denormalized onto `list_items`, `check_off_events`, `store_aisles`, `store_items`.
- pg_trgm GIN on name columns; no tsvector FTS in v1.
- DB-backed tests **skip cleanly** when no Postgres/docker is available.
- Schema + migrations + runner only — no row structs, repositories, or queries.
- Conventional Commits; **no AI attribution**; commit on branch `feat/issue-2-postgres-schema` (never `main`).

## File structure

- `backend/migrations/0001_init.up.sql` — extensions, 11 tables, indexes.
- `backend/migrations/0001_init.down.sql` — drop everything (reverse order).
- `backend/migrations/embed.go` — `package migrations`; `//go:embed *.sql` → `var FS embed.FS`.
- `backend/internal/db/migrate.go` — `Migrate(ctx, databaseURL) error`.
- `backend/internal/db/migrate_test.go` — testcontainers round-trip.
- `backend/migrations/.gitkeep` — **delete** (real files now exist).

---

### Task 1: The `0001_init` SQL migration (up + down)

**Files:**
- Create: `backend/migrations/0001_init.up.sql`, `backend/migrations/0001_init.down.sql`
- Delete: `backend/migrations/.gitkeep`

**Interfaces:**
- Produces: 11 tables — `households, users, lists, items, list_items, check_off_events, stores, store_aisles, store_items, food_catalog, ean_mappings` — applied by golang-migrate version `1`.

This task has no Go test of its own; it is verified end-to-end by Task 3. Keep the SQL self-contained and ordered so foreign keys resolve.

- [ ] **Step 1: Write `backend/migrations/0001_init.up.sql`**

```sql
-- v1 schema. Greenfield. All identifiers English.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE households (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

CREATE TABLE users (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id    text NOT NULL UNIQUE,
    display_name text,
    household_id uuid REFERENCES households(id),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    deleted_at   timestamptz
);

CREATE TABLE lists (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    household_id uuid NOT NULL REFERENCES households(id),
    name         text NOT NULL,
    archived_at  timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    deleted_at   timestamptz
);

CREATE TABLE items (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    household_id      uuid NOT NULL REFERENCES households(id),
    name              text NOT NULL,
    aisle             int,
    image_url         text,
    source            text,
    external_id       text,
    purchase_count    int NOT NULL DEFAULT 0,
    last_purchased_at timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    deleted_at        timestamptz
);

CREATE TABLE list_items (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    household_id uuid NOT NULL REFERENCES households(id),
    list_id      uuid NOT NULL REFERENCES lists(id),
    item_id      uuid REFERENCES items(id),
    name         text NOT NULL,
    quantity     int NOT NULL DEFAULT 1,
    note         text,
    aisle        int,
    position     int NOT NULL DEFAULT 0,
    checked_at   timestamptz,
    checked_by   uuid REFERENCES users(id),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    deleted_at   timestamptz
);

CREATE TABLE stores (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    household_id uuid NOT NULL REFERENCES households(id),
    name         text NOT NULL,
    chain        text,
    place_id     text,
    osm_id       text,
    latitude     double precision,
    longitude    double precision,
    address      text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    deleted_at   timestamptz,
    UNIQUE (household_id, place_id)
);

CREATE TABLE store_aisles (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    household_id uuid NOT NULL REFERENCES households(id),
    store_id     uuid NOT NULL REFERENCES stores(id),
    aisle        int NOT NULL,
    position     int NOT NULL DEFAULT 0,
    label        text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    deleted_at   timestamptz,
    UNIQUE (store_id, aisle)
);

CREATE TABLE store_items (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    household_id uuid NOT NULL REFERENCES households(id),
    store_id     uuid NOT NULL REFERENCES stores(id),
    item_id      uuid NOT NULL REFERENCES items(id),
    aisle        int,
    position     int,
    available    boolean NOT NULL DEFAULT true,
    last_seen_at timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    deleted_at   timestamptz,
    UNIQUE (store_id, item_id)
);

CREATE TABLE check_off_events (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    household_id uuid NOT NULL REFERENCES households(id),
    list_item_id uuid REFERENCES list_items(id),
    user_id      uuid REFERENCES users(id),
    item_id      uuid REFERENCES items(id),
    store_id     uuid REFERENCES stores(id),
    quantity     int NOT NULL DEFAULT 1,
    checked_at   timestamptz NOT NULL DEFAULT now(),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    deleted_at   timestamptz
);

CREATE TABLE food_catalog (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    source      text NOT NULL,
    external_id text,
    name        text NOT NULL,
    food_group  text,
    aisle       int,
    image_url   text,
    raw         jsonb,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    deleted_at  timestamptz,
    UNIQUE (source, external_id)
);

CREATE TABLE ean_mappings (
    ean        text PRIMARY KEY,
    name       text NOT NULL,
    brand      text,
    aisle      int,
    image_url  text,
    source     text NOT NULL,
    raw        jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

-- Indexes: autocomplete
CREATE INDEX items_name_trgm        ON items       USING gin (name gin_trgm_ops);
CREATE INDEX food_catalog_name_trgm ON food_catalog USING gin (name gin_trgm_ops);
CREATE INDEX items_autocomplete     ON items (household_id, purchase_count DESC, last_purchased_at DESC);

-- Indexes: pull-sync cursor + household scoping
CREATE INDEX lists_sync            ON lists            (household_id, updated_at);
CREATE INDEX items_sync            ON items            (household_id, updated_at);
CREATE INDEX list_items_sync       ON list_items       (household_id, updated_at);
CREATE INDEX check_off_events_sync ON check_off_events (household_id, updated_at);
CREATE INDEX users_sync            ON users            (household_id, updated_at);
CREATE INDEX stores_sync           ON stores           (household_id, updated_at);
CREATE INDEX store_aisles_sync     ON store_aisles     (household_id, updated_at);
CREATE INDEX store_items_sync      ON store_items      (household_id, updated_at);

-- Indexes: lookups
CREATE INDEX list_items_list           ON list_items       (list_id);
CREATE INDEX check_off_events_store    ON check_off_events (store_id, item_id);
```

- [ ] **Step 2: Write `backend/migrations/0001_init.down.sql`**

```sql
-- Reverse of 0001_init.up.sql. Drop in FK-safe order.
DROP TABLE IF EXISTS ean_mappings;
DROP TABLE IF EXISTS food_catalog;
DROP TABLE IF EXISTS check_off_events;
DROP TABLE IF EXISTS store_items;
DROP TABLE IF EXISTS store_aisles;
DROP TABLE IF EXISTS stores;
DROP TABLE IF EXISTS list_items;
DROP TABLE IF EXISTS items;
DROP TABLE IF EXISTS lists;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS households;
DROP EXTENSION IF EXISTS pg_trgm;
```

- [ ] **Step 3: Remove the placeholder**

```bash
git rm backend/migrations/.gitkeep
```

- [ ] **Step 4: Sanity-check the SQL parses (optional, requires docker)**

Run:
```bash
docker run --rm -i postgres:16-alpine sh -c 'initdb -D /tmp/d >/dev/null 2>&1; pg_ctl -D /tmp/d -o "-k /tmp" -w start >/dev/null 2>&1; psql -h /tmp -U postgres -v ON_ERROR_STOP=1 -f -' < backend/migrations/0001_init.up.sql && echo "UP PARSES"
```
Expected: `UP PARSES` (or skip — Task 3 verifies authoritatively). If docker is unavailable, skip this step.

- [ ] **Step 5: Commit**

```bash
git add backend/migrations/0001_init.up.sql backend/migrations/0001_init.down.sql
git rm --cached backend/migrations/.gitkeep 2>/dev/null; rm -f backend/migrations/.gitkeep
git add backend/migrations/.gitkeep 2>/dev/null
git commit -m "feat(db): add 0001_init schema migration (11 tables)"
```

---

### Task 2: Embed migrations + the `Migrate` runner

**Files:**
- Create: `backend/migrations/embed.go`, `backend/internal/db/migrate.go`

**Interfaces:**
- Consumes: the SQL files from Task 1.
- Produces:
  - `package migrations` with `var FS embed.FS` (embeds `*.sql`).
  - `func db.Migrate(ctx context.Context, databaseURL string) error` — applies all up migrations; returns nil on success **and** when already up to date.

- [ ] **Step 1: Add dependencies**

```bash
cd backend
go get github.com/golang-migrate/migrate/v4@latest
go get github.com/golang-migrate/migrate/v4/source/iofs@latest
go get github.com/golang-migrate/migrate/v4/database/pgx/v5@latest
go get github.com/jackc/pgx/v5@latest
```

- [ ] **Step 2: Write `backend/migrations/embed.go`**

```go
// Package migrations embeds the SQL migration files so they travel with the
// compiled binary (including the Lambda artifact) — no runtime file path needed.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
```

- [ ] **Step 3: Write `backend/internal/db/migrate.go`**

```go
// Package db owns database schema migration. Query/repository code lives with
// each domain package, not here.
package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/stroem/shopping-list/backend/migrations"
)

// Migrate applies all pending up migrations to the database at databaseURL.
// It is safe to call on every startup: an already-migrated database is a no-op.
// databaseURL must use the pgx scheme, e.g. "pgx5://user:pass@host:5432/db".
func Migrate(ctx context.Context, databaseURL string) error {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("load embedded migrations: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, databaseURL)
	if err != nil {
		return fmt.Errorf("init migrator: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Verify it builds and vets**

Run: `cd backend && go build ./... && go vet ./...`
Expected: success (no test yet — that is Task 3).

- [ ] **Step 5: Commit**

```bash
git add backend/migrations/embed.go backend/internal/db/migrate.go backend/go.mod backend/go.sum
git commit -m "feat(db): embed migrations and add Migrate runner"
```

---

### Task 3: testcontainers round-trip test (AC4)

**Files:**
- Create: `backend/internal/db/migrate_test.go`

**Interfaces:**
- Consumes: `db.Migrate`, `migrations.FS`.
- Produces: the green-bar proof that up creates all 11 tables and down removes them.

**Note on the database URL scheme:** `db.Migrate` and the test build a migrate URL with the **`pgx5://`** scheme (the driver registered by `database/pgx/v5`). testcontainers' `ConnectionString` returns a `postgres://...` URL; rewrite the scheme to `pgx5://` for migrate, and use the original `postgres://` for the `pgx.Connect` assertions.

- [ ] **Step 1: Add the test dependency**

```bash
cd backend
go get github.com/testcontainers/testcontainers-go@latest
go get github.com/testcontainers/testcontainers-go/modules/postgres@latest
```

- [ ] **Step 2: Write the failing test**

Create `backend/internal/db/migrate_test.go`:

```go
package db_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/stroem/shopping-list/backend/internal/db"
)

var wantTables = []string{
	"households", "users", "lists", "items", "list_items",
	"check_off_events", "stores", "store_aisles", "store_items",
	"food_catalog", "ean_mappings",
}

func TestMigrateUpDownRoundTrip(t *testing.T) {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("shopping_list"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("5432/tcp").WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Skipf("skipping: cannot start postgres container (docker unavailable?): %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })

	pgURL, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	migrateURL := "pgx5://" + strings.TrimPrefix(pgURL, "postgres://")

	// Up
	if err := db.Migrate(ctx, migrateURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	if got := countTables(t, ctx, pgURL, wantTables); got != len(wantTables) {
		t.Fatalf("after up: %d of %d expected tables present", got, len(wantTables))
	}

	// Down
	if err := migrateDown(migrateURL); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	if got := countTables(t, ctx, pgURL, wantTables); got != 0 {
		t.Fatalf("after down: %d expected tables still present, want 0", got)
	}
}
```

- [ ] **Step 3: Add the test helpers (same file)**

`migrateDown` reuses the embedded source; add a small exported `db.MigrateDown` to keep the migrate wiring in one place, then call it from the test.

First add to `backend/internal/db/migrate.go`:

```go
// MigrateDown reverts all migrations. Intended for tests; not called in production.
func MigrateDown(databaseURL string) error {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("load embedded migrations: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, databaseURL)
	if err != nil {
		return fmt.Errorf("init migrator: %w", err)
	}
	defer m.Close()
	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("revert migrations: %w", err)
	}
	return nil
}
```

Then add the helpers to `migrate_test.go`:

```go
func migrateDown(migrateURL string) error {
	return db.MigrateDown(migrateURL)
}

func countTables(t *testing.T, ctx context.Context, pgURL string, names []string) int {
	t.Helper()
	conn, err := pgx.Connect(ctx, pgURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	var n int
	err = conn.QueryRow(ctx, `
		SELECT count(*) FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name = ANY($1)`, names).Scan(&n)
	if err != nil {
		t.Fatalf("count tables: %v", err)
	}
	return n
}
```

- [ ] **Step 4: Run the test (docker present → runs; absent → skips)**

Run: `cd backend && go test ./internal/db/ -run TestMigrateUpDownRoundTrip -v`
Expected: PASS (`+1`), or `SKIP` with the docker-unavailable message. With docker up here, expect PASS.

- [ ] **Step 5: Run the full suite + vet**

Run: `cd backend && go build ./... && go vet ./... && go test ./...`
Expected: all green (router test from #1 still passes).

- [ ] **Step 6: Commit**

```bash
git add backend/internal/db/migrate.go backend/internal/db/migrate_test.go backend/go.mod backend/go.sum
git commit -m "test(db): round-trip migrations up and down against postgres"
```

---

## Self-Review

**Spec coverage:**
- 11 tables with English columns, soft-delete + `updated_at`, UUID defaults, `image_url`/`source`/`external_id`/`raw jsonb`, store tables + `check_off_events.store_id` → Task 1. ✓
- pg_trgm GIN + autocomplete + `(household_id, updated_at)` sync indexes + uniques → Task 1 index block. ✓
- golang-migrate, embedded, reusable `Migrate` runner → Task 2. ✓
- AC4 up/down round-trip via testcontainers, skip without docker → Task 3. ✓
- Out of scope (no structs/repos/queries) honored — only `Migrate`/`MigrateDown` runners. ✓

**Placeholder scan:** No TBD/TODO; every SQL and Go block is complete. The optional docker sanity-check (Task 1 Step 4) is explicitly optional with a fallback. ✓

**Type consistency:** `db.Migrate(ctx, databaseURL)` and `db.MigrateDown(databaseURL)` are defined in Task 2/3 and consumed identically in the test. `migrations.FS` name matches across `embed.go` and both runners. `wantTables` lists exactly the 11 `CREATE TABLE` names from Task 1. The `pgx5://` scheme matches the `database/pgx/v5` driver registration. ✓
