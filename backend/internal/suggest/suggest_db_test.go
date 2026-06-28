package suggest_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/stroem/shopping-list/backend/internal/db"
	"github.com/stroem/shopping-list/backend/internal/suggest"
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

func names(sugs []suggest.Suggestion) []string {
	out := make([]string, len(sugs))
	for i, s := range sugs {
		out[i] = s.Name
	}
	return out
}

func TestSuggestRankingAndIsolation(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)

	// Two households, each with a device. Household A has bought "Mjölk" twice.
	// pgx's default Exec uses the extended protocol, which rejects multiple
	// statements per call — so seed one statement at a time.
	seed := []string{
		`INSERT INTO households (id) VALUES
			('11111111-1111-1111-1111-111111111111'),
			('22222222-2222-2222-2222-222222222222')`,
		`INSERT INTO users (device_id, household_id) VALUES
			('devA', '11111111-1111-1111-1111-111111111111'),
			('devB', '22222222-2222-2222-2222-222222222222')`,
		`INSERT INTO items (household_id, name, aisle, purchase_count) VALUES
			('11111111-1111-1111-1111-111111111111', 'Mjölk', 2, 5),
			('22222222-2222-2222-2222-222222222222', 'Mjölk hemlig B', 2, 9)`,
		`INSERT INTO food_catalog (source, external_id, name, aisle) VALUES
			('livsmedelsverket', '1', 'Mjölk 3%', 2),
			('livsmedelsverket', '2', 'Mjölkchoklad', 9)`,
		`INSERT INTO ean_mappings (ean, name, aisle, source) VALUES
			('73100', 'Mjölk Arla', 2, 'openfoodfacts')`,
	}
	for _, stmt := range seed {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	s := suggest.New(pool)

	// Household A: its own item ranks first; results deduped; B's item absent.
	got, err := s.Suggest(ctx, "devA", "Mjölk", 10)
	if err != nil {
		t.Fatalf("suggest A: %v", err)
	}
	if len(got) == 0 || got[0].Name != "Mjölk" || got[0].Source != "items" {
		t.Fatalf("A first = %+v, want items 'Mjölk' first; all=%v", firstOrZero(got), names(got))
	}
	for _, sug := range got {
		if sug.Name == "Mjölk hemlig B" {
			t.Fatalf("household leak: A saw B's item; all=%v", names(got))
		}
		if sug.Source == "openfoodfacts" && sug.EAN == nil {
			t.Fatalf("branded row missing ean: %+v", sug)
		}
		if sug.Source != "openfoodfacts" && sug.EAN != nil {
			t.Fatalf("non-branded row has ean: %+v", sug)
		}
	}

	// Unknown device → catalog-only (no items source at all).
	got, err = s.Suggest(ctx, "ghost", "Mjölk", 10)
	if err != nil {
		t.Fatalf("suggest ghost: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("ghost got nothing, want catalog results")
	}
	for _, sug := range got {
		if sug.Source == "items" {
			t.Fatalf("unknown device saw items source: %v", names(got))
		}
	}

	// limit honored.
	got, err = s.Suggest(ctx, "devA", "Mjölk", 1)
	if err != nil || len(got) != 1 {
		t.Fatalf("limit=1 → %d results err=%v, want 1", len(got), err)
	}
}

func firstOrZero(s []suggest.Suggestion) suggest.Suggestion {
	if len(s) == 0 {
		return suggest.Suggestion{}
	}
	return s[0]
}
