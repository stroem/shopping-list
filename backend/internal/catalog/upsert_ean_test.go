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

func strpt(s string) *string { return &s }

func TestUpsertEANIsIdempotent(t *testing.T) {
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

	rows := []catalog.EanRow{
		{EAN: "111", Name: "Mellanmjölk", Source: "openfoodfacts", Aisle: intp(2),
			NutriscoreGrade: strpt("b"), Allergens: []string{"milk"}, Labels: []string{},
			Nutriments: catalog.Nutriments{}, Ingredients: catalog.Ingredients{List: []string{}}},
		{EAN: "222", Name: "Lax", Source: "openfoodfacts", Aisle: intp(4),
			Allergens: []string{"fish"}, Labels: []string{},
			Nutriments: catalog.Nutriments{}, Ingredients: catalog.Ingredients{List: []string{}}},
	}

	ins, upd, err := catalog.UpsertEAN(ctx, pool, rows)
	if err != nil || ins != 2 || upd != 0 {
		t.Fatalf("first upsert: ins=%d upd=%d err=%v, want 2/0/nil", ins, upd, err)
	}

	ins, upd, err = catalog.UpsertEAN(ctx, pool, rows)
	if err != nil || ins != 0 || upd != 2 {
		t.Fatalf("second upsert: ins=%d upd=%d err=%v, want 0/2/nil", ins, upd, err)
	}

	// Changed field updates in place; row count stays 1; jsonb round-trips.
	rows[0].NutriscoreGrade = strpt("c")
	if _, _, err := catalog.UpsertEAN(ctx, pool, rows); err != nil {
		t.Fatalf("third upsert: %v", err)
	}
	var count int
	var grade string
	var allergens []byte
	if err := pool.QueryRow(ctx,
		`SELECT count(*) OVER (), nutriscore_grade, allergens FROM ean_mappings WHERE ean='111'`,
	).Scan(&count, &grade, &allergens); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if count != 1 || grade != "c" || string(allergens) != `["milk"]` {
		t.Fatalf("after update: count=%d grade=%q allergens=%s", count, grade, allergens)
	}
}
