package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/stroem/shopping-list/backend/internal/auth"
	"github.com/stroem/shopping-list/backend/internal/db"
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

func TestUserStoreUpsertAutoCreates(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	s := auth.NewUserStore(pool)

	uid, hh, err := s.Upsert(ctx, "https://accounts.google.com", "sub-1", "a@example.com")
	if err != nil || uid == "" {
		t.Fatalf("first upsert: uid=%q err=%v", uid, err)
	}
	if hh != nil {
		t.Fatalf("new user should have no household, got %v", *hh)
	}

	// Same identity → same row (id stable), email updated.
	uid2, _, err := s.Upsert(ctx, "https://accounts.google.com", "sub-1", "new@example.com")
	if err != nil || uid2 != uid {
		t.Fatalf("second upsert: uid2=%q (want %q) err=%v", uid2, uid, err)
	}
	var count int
	var email string
	if err := pool.QueryRow(ctx,
		`SELECT count(*) OVER (), email FROM users WHERE issuer=$1 AND subject=$2`,
		"https://accounts.google.com", "sub-1").Scan(&count, &email); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if count != 1 || email != "new@example.com" {
		t.Fatalf("after re-upsert: count=%d email=%q", count, email)
	}
}
