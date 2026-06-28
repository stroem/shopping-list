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
	if err := db.MigrateDown(migrateURL); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	if got := countTables(t, ctx, pgURL, wantTables); got != 0 {
		t.Fatalf("after down: %d expected tables still present, want 0", got)
	}
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
