package idempotency_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/stroem/shopping-list/backend/internal/db"
	"github.com/stroem/shopping-list/backend/internal/idempotency"
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

// makeHousehold inserts a household row and returns its id (idempotency_keys FKs households).
func makeHousehold(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO households (name) VALUES ('t') RETURNING id::text`).Scan(&id); err != nil {
		t.Fatalf("insert household: %v", err)
	}
	return id
}

func TestStoreRoundTrip(t *testing.T) {
	pool := newPool(t)
	s := idempotency.NewStore(pool)
	ctx := context.Background()
	hh := makeHousehold(t, pool)

	if _, found, err := s.Lookup(ctx, hh, "k1"); err != nil || found {
		t.Fatalf("miss: found=%v err=%v", found, err)
	}
	if err := s.Save(ctx, hh, "k1", "POST", "/v1/x", 201, []byte(`{"id":"a"}`)); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, found, err := s.Lookup(ctx, hh, "k1")
	if err != nil || !found {
		t.Fatalf("hit: found=%v err=%v", found, err)
	}
	if got.StatusCode != 201 || string(got.Body) != `{"id":"a"}` {
		t.Fatalf("round-trip = %d %q", got.StatusCode, got.Body)
	}
	// Duplicate save is a no-op (keeps first).
	if err := s.Save(ctx, hh, "k1", "POST", "/v1/x", 500, []byte(`changed`)); err != nil {
		t.Fatalf("dup save: %v", err)
	}
	got, _, _ = s.Lookup(ctx, hh, "k1")
	if got.StatusCode != 201 || string(got.Body) != `{"id":"a"}` {
		t.Fatalf("dup save must not overwrite: %d %q", got.StatusCode, got.Body)
	}
}
