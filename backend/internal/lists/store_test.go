package lists_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/stroem/shopping-list/backend/internal/db"
	"github.com/stroem/shopping-list/backend/internal/lists"
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

// mkHousehold inserts a household and returns its id; lists are scoped to it.
func mkHousehold(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO households (name) VALUES ('Home') RETURNING id::text`,
	).Scan(&id); err != nil {
		t.Fatalf("mkHousehold: %v", err)
	}
	return id
}

func TestUpsertCreateThenIdempotentAndGet(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	s := lists.NewStore(pool)
	hh := mkHousehold(t, pool)
	id := "11111111-1111-1111-1111-111111111111"

	// First PUT creates.
	l, created, err := s.Upsert(ctx, hh, id, "Groceries")
	if err != nil || !created {
		t.Fatalf("create: created=%v err=%v", created, err)
	}
	if l.ID != id || l.Name != "Groceries" || l.ArchivedAt != nil || l.DeletedAt != nil {
		t.Fatalf("created list = %+v", l)
	}

	// Replaying the same PUT is idempotent: no new row, created=false.
	l2, created2, err := s.Upsert(ctx, hh, id, "Groceries")
	if err != nil || created2 {
		t.Fatalf("idempotent repeat: created=%v err=%v", created2, err)
	}
	if l2.ID != id {
		t.Fatalf("repeat id = %q", l2.ID)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM lists WHERE id = $1::uuid`, id).Scan(&count); err != nil || count != 1 {
		t.Fatalf("row count = %d err=%v, want 1 (no duplicate)", count, err)
	}

	// Get returns it, scoped to the household.
	g, err := s.Get(ctx, hh, id)
	if err != nil || g.ID != id || g.Name != "Groceries" {
		t.Fatalf("get: %+v err=%v", g, err)
	}

	// Get of a missing id → ErrNotFound.
	if _, err := s.Get(ctx, hh, "22222222-2222-2222-2222-222222222222"); !errors.Is(err, lists.ErrNotFound) {
		t.Fatalf("get missing err = %v, want ErrNotFound", err)
	}
}
