package households_test

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
	"github.com/stroem/shopping-list/backend/internal/households"
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

func mkUser(t *testing.T, pool *pgxpool.Pool, sub string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (issuer, subject) VALUES ('iss', $1) RETURNING id::text`, sub,
	).Scan(&id); err != nil {
		t.Fatalf("mkUser: %v", err)
	}
	return id
}

func TestHouseholdCreateJoinGet(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	s := households.NewStore(pool)

	userA := mkUser(t, pool, "subA")
	userB := mkUser(t, pool, "subB")

	name := "Home"
	hh, err := s.Create(ctx, userA, &name)
	if err != nil || hh.ID == "" || hh.InviteCode == "" {
		t.Fatalf("create: %+v err=%v", hh, err)
	}
	// userA is now in the household.
	var aHH string
	if err := pool.QueryRow(ctx, `SELECT household_id::text FROM users WHERE id=$1::uuid`, userA).Scan(&aHH); err != nil || aHH != hh.ID {
		t.Fatalf("userA household = %q want %q err=%v", aHH, hh.ID, err)
	}

	// userB joins by code → same household.
	joined, err := s.JoinByCode(ctx, userB, hh.InviteCode)
	if err != nil || joined.ID != hh.ID {
		t.Fatalf("join: %+v err=%v", joined, err)
	}

	// Bad code → ErrNotFound.
	if _, err := s.JoinByCode(ctx, userB, "nope-nope"); !errors.Is(err, households.ErrNotFound) {
		t.Fatalf("join bad code err = %v, want ErrNotFound", err)
	}

	// Get existing / missing.
	if g, err := s.Get(ctx, hh.ID); err != nil || g.InviteCode != hh.InviteCode {
		t.Fatalf("get: %+v err=%v", g, err)
	}
	if _, err := s.Get(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, households.ErrNotFound) {
		t.Fatalf("get missing err = %v, want ErrNotFound", err)
	}
}
