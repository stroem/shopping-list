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

	// Two households, each with a device. pgx's default Exec uses the extended
	// protocol, which rejects multiple statements per call — seed one at a time.
	// Household A items exercise frequency (pc) then recency (last_purchased_at):
	//   Mjölksyra pc9 newest, Mjölk pc9 older, Mjölkfil pc3 → that exact order.
	// food_catalog has a literal "Mjölk" (must be deduped under the household
	// item) and a fuzzy-only "Lättmjölk" (no prefix match).
	seed := []string{
		`INSERT INTO households (id) VALUES
			('11111111-1111-1111-1111-111111111111'),
			('22222222-2222-2222-2222-222222222222')`,
		`INSERT INTO users (device_id, household_id) VALUES
			('devA', '11111111-1111-1111-1111-111111111111'),
			('devB', '22222222-2222-2222-2222-222222222222')`,
		`INSERT INTO items (household_id, name, aisle, purchase_count, last_purchased_at) VALUES
			('11111111-1111-1111-1111-111111111111', 'Mjölk',     2, 9, now() - interval '2 days'),
			('11111111-1111-1111-1111-111111111111', 'Mjölksyra', 2, 9, now()),
			('11111111-1111-1111-1111-111111111111', 'Mjölkfil',  2, 3, now()),
			('22222222-2222-2222-2222-222222222222', 'Mjölk hemlig B', 2, 50, now())`,
		`INSERT INTO food_catalog (source, external_id, name, aisle) VALUES
			('livsmedelsverket', '1', 'Mjölk 3%', 2),
			('livsmedelsverket', '2', 'Mjölk', 2),
			('livsmedelsverket', '3', 'Lättmjölk', 2),
			('livsmedelsverket', '4', 'Mjölkchoklad', 9)`,
		`INSERT INTO ean_mappings (ean, name, aisle, source) VALUES
			('73100', 'Mjölk Arla', 2, 'openfoodfacts')`,
	}
	for _, stmt := range seed {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	s := suggest.New(pool)

	got, err := s.Suggest(ctx, "devA", "Mjölk", 25)
	if err != nil {
		t.Fatalf("suggest A: %v", err)
	}

	// Household items come first, ordered by purchase_count DESC then recency:
	// Mjölksyra (pc9, newest) → Mjölk (pc9, older) → Mjölkfil (pc3).
	wantItemPrefix := []string{"Mjölksyra", "Mjölk", "Mjölkfil"}
	all := names(got)
	if len(all) < 3 || all[0] != wantItemPrefix[0] || all[1] != wantItemPrefix[1] || all[2] != wantItemPrefix[2] {
		t.Fatalf("items order = %v, want first three %v (pc then recency)", all, wantItemPrefix)
	}
	for i := 0; i < 3; i++ {
		if got[i].Source != "items" {
			t.Fatalf("result %d (%s) source = %q, want items", i, got[i].Name, got[i].Source)
		}
	}

	// Dedup: the catalog "Mjölk" collapses under the household item — exactly one
	// "Mjölk", sourced from items.
	mjolk := 0
	for _, sug := range got {
		if sug.Name == "Mjölk" {
			mjolk++
			if sug.Source != "items" {
				t.Fatalf("'Mjölk' kept from %q, want items (household wins dedup)", sug.Source)
			}
		}
		if sug.Name == "Mjölk hemlig B" {
			t.Fatalf("household leak: A saw B's item; all=%v", all)
		}
		if sug.Source == "openfoodfacts" && sug.EAN == nil {
			t.Fatalf("branded row missing ean: %+v", sug)
		}
		if sug.Source != "openfoodfacts" && sug.EAN != nil {
			t.Fatalf("non-branded row has ean: %+v", sug)
		}
	}
	if mjolk != 1 {
		t.Fatalf("'Mjölk' appeared %d times, want 1 (dedup); all=%v", mjolk, all)
	}

	// Prefix beats fuzzy: the prefix match "Mjölk 3%" outranks the fuzzy-only
	// "Lättmjölk" within the catalog tier.
	if p, f := indexOf(all, "Mjölk 3%"), indexOf(all, "Lättmjölk"); p == -1 {
		t.Fatalf("'Mjölk 3%%' missing from results: %v", all)
	} else if f != -1 && p > f {
		t.Fatalf("prefix 'Mjölk 3%%' (%d) ranked after fuzzy 'Lättmjölk' (%d); all=%v", p, f, all)
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

func indexOf(ss []string, want string) int {
	for i, s := range ss {
		if s == want {
			return i
		}
	}
	return -1
}
