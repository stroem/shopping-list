package sync_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/stroem/shopping-list/backend/internal/db"
	syncpkg "github.com/stroem/shopping-list/backend/internal/sync"
)

func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
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
	if err := db.Migrate(ctx, pgURL); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxpool.New(ctx, pgURL)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func mkHousehold(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO households (name) VALUES ('h') RETURNING id::text`).Scan(&id); err != nil {
		t.Fatalf("household: %v", err)
	}
	return id
}

func TestChangesScopesAndSoftDeletes(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	hhA := mkHousehold(t, pool)
	hhB := mkHousehold(t, pool)

	// Two lists in A (one soft-deleted), one in B.
	mustExec(t, pool, `INSERT INTO lists (household_id, name) VALUES ($1::uuid,'A-live')`, hhA)
	mustExec(t, pool, `INSERT INTO lists (household_id, name, deleted_at) VALUES ($1::uuid,'A-gone', now())`, hhA)
	mustExec(t, pool, `INSERT INTO lists (household_id, name) VALUES ($1::uuid,'B-live')`, hhB)

	s := syncpkg.NewStore(pool)
	res, err := s.Changes(ctx, hhA, time.Time{}) // full sync
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	lists := res.Changes["lists"]
	if len(lists) != 2 {
		t.Fatalf("A lists = %d, want 2 (incl. soft-deleted)", len(lists))
	}
	for _, l := range lists {
		if l["name"] == "B-live" {
			t.Fatal("cross-household leak: B row returned for A")
		}
	}
	// Soft-deleted row carries deleted_at.
	var sawDeleted bool
	for _, l := range lists {
		if l["name"] == "A-gone" && l["deleted_at"] != nil {
			sawDeleted = true
		}
	}
	if !sawDeleted {
		t.Fatal("soft-deleted row missing or deleted_at nil")
	}
	// Every entity key present with a (possibly empty) array.
	for _, key := range []string{"lists", "items", "list_items", "check_off_events", "users", "stores", "store_aisles", "store_items"} {
		if _, ok := res.Changes[key]; !ok {
			t.Fatalf("missing entity key %q in response", key)
		}
	}
}

func TestChangesCursorAdvancesAndIsStable(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	hh := mkHousehold(t, pool)
	mustExec(t, pool, `INSERT INTO lists (household_id, name) VALUES ($1::uuid,'one')`, hh)

	s := syncpkg.NewStore(pool)
	res, err := s.Changes(ctx, hh, time.Time{})
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	if len(res.Changes["lists"]) != 1 || res.Cursor.IsZero() {
		t.Fatalf("first sync: %d lists, cursor zero=%v", len(res.Changes["lists"]), res.Cursor.IsZero())
	}
	// Re-pull from the returned cursor → nothing new (stable).
	res2, err := s.Changes(ctx, hh, res.Cursor)
	if err != nil {
		t.Fatalf("re-pull: %v", err)
	}
	if len(res2.Changes["lists"]) != 0 {
		t.Fatalf("re-pull returned %d lists, want 0", len(res2.Changes["lists"]))
	}
}

func mustExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}
