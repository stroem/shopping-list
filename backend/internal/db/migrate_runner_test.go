package db_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/stroem/shopping-list/backend/internal/db"
)

// startPostgres boots a throwaway Postgres and returns its URL, skipping the
// test when Docker is unavailable (mirrors TestMigrateUpDownRoundTrip).
func startPostgres(t *testing.T) string {
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
	url, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	return url
}

func TestVersionNoMigrations(t *testing.T) {
	url := startPostgres(t)
	if _, _, err := db.Version(url); !errors.Is(err, db.ErrNoMigrations) {
		t.Fatalf("Version on fresh db: got err %v, want ErrNoMigrations", err)
	}
}

func TestVersionAfterUp(t *testing.T) {
	url := startPostgres(t)
	if err := db.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	v, dirty, err := db.Version(url)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if v != 2 || dirty {
		t.Fatalf("Version after up = (%d, dirty=%v), want (2, false)", v, dirty)
	}
}

func TestStepsForwardAndBack(t *testing.T) {
	url := startPostgres(t)
	if err := db.Steps(url, 1); err != nil {
		t.Fatalf("Steps(+1): %v", err)
	}
	if v, _, _ := db.Version(url); v != 1 {
		t.Fatalf("after Steps(+1) version = %d, want 1", v)
	}
	if err := db.Steps(url, 1); err != nil {
		t.Fatalf("Steps(+1) again: %v", err)
	}
	if v, _, _ := db.Version(url); v != 2 {
		t.Fatalf("after second Steps(+1) version = %d, want 2", v)
	}
	if err := db.Steps(url, -1); err != nil {
		t.Fatalf("Steps(-1): %v", err)
	}
	if v, _, _ := db.Version(url); v != 1 {
		t.Fatalf("after Steps(-1) version = %d, want 1", v)
	}
}

func TestGotoUpThenZeroReverts(t *testing.T) {
	url := startPostgres(t)
	if err := db.Goto(url, 2); err != nil {
		t.Fatalf("Goto(2): %v", err)
	}
	if v, _, _ := db.Version(url); v != 2 {
		t.Fatalf("after Goto(2) version = %d, want 2", v)
	}
	if err := db.Goto(url, 0); err != nil {
		t.Fatalf("Goto(0): %v", err)
	}
	if _, _, err := db.Version(url); !errors.Is(err, db.ErrNoMigrations) {
		t.Fatalf("after Goto(0): got err %v, want ErrNoMigrations", err)
	}
}

func TestForceClearsDirty(t *testing.T) {
	url := startPostgres(t)
	if err := db.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	// Simulate an interrupted migration: mark the version row dirty.
	setDirty(t, url)
	if _, dirty, err := db.Version(url); err != nil || !dirty {
		t.Fatalf("expected dirty version, got dirty=%v err=%v", dirty, err)
	}
	if err := db.Force(url, 2); err != nil {
		t.Fatalf("Force(2): %v", err)
	}
	v, dirty, err := db.Version(url)
	if err != nil || v != 2 || dirty {
		t.Fatalf("after Force(2) = (%d, dirty=%v, err=%v), want (2, false, nil)", v, dirty, err)
	}
}

func setDirty(t *testing.T, url string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `UPDATE schema_migrations SET dirty = true`); err != nil {
		t.Fatalf("set dirty: %v", err)
	}
}
