package catalog_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/stroem/shopping-list/backend/internal/catalog"
	"github.com/stroem/shopping-list/backend/internal/db"
)

func intp(i int) *int { return &i }

func TestUpsertFoodIsIdempotent(t *testing.T) {
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
	defer pool.Close()

	rows := []catalog.Row{
		{Source: "livsmedelsverket", ExternalID: "1", Name: "Mjölk 3%", Aisle: intp(2)},
		{Source: "livsmedelsverket", ExternalID: "2", Name: "Lax", Aisle: intp(4)},
	}

	ins, upd, err := catalog.UpsertFood(ctx, pool, rows)
	if err != nil || ins != 2 || upd != 0 {
		t.Fatalf("first upsert: ins=%d upd=%d err=%v, want 2/0/nil", ins, upd, err)
	}

	// Re-run identical → all updates, no inserts (idempotent).
	ins, upd, err = catalog.UpsertFood(ctx, pool, rows)
	if err != nil || ins != 0 || upd != 2 {
		t.Fatalf("second upsert: ins=%d upd=%d err=%v, want 0/2/nil", ins, upd, err)
	}

	// Changed name updates in place; still one row per external_id.
	rows[0].Name = "Mjölk 1.5%"
	if _, _, err = catalog.UpsertFood(ctx, pool, rows); err != nil {
		t.Fatalf("third upsert: %v", err)
	}
	var name string
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*), max(name) FROM food_catalog WHERE source='livsmedelsverket' AND external_id='1'`).Scan(&count, &name); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if count != 1 || name != "Mjölk 1.5%" {
		t.Fatalf("after update: count=%d name=%q, want 1 / Mjölk 1.5%%", count, name)
	}
}
