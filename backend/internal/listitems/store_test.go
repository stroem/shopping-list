package listitems_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/stroem/shopping-list/backend/internal/catalog"
	"github.com/stroem/shopping-list/backend/internal/db"
	"github.com/stroem/shopping-list/backend/internal/listitems"
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

// mkList inserts a list under householdID and returns its id. When deleted is
// true the row is soft-deleted, so it must not be a valid Add target.
func mkList(t *testing.T, pool *pgxpool.Pool, householdID string, deleted bool) string {
	t.Helper()
	var id string
	del := "NULL"
	if deleted {
		del = "now()"
	}
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO lists (household_id, name, deleted_at)
		 VALUES ($1::uuid, 'Groceries', `+del+`) RETURNING id::text`,
		householdID,
	).Scan(&id); err != nil {
		t.Fatalf("mkList: %v", err)
	}
	return id
}

// mkItem inserts an items-master row under householdID and returns its id; used
// to build a foreign item_id reference.
func mkItem(t *testing.T, pool *pgxpool.Pool, householdID, name string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO items (household_id, name) VALUES ($1::uuid, $2) RETURNING id::text`,
		householdID, name,
	).Scan(&id); err != nil {
		t.Fatalf("mkItem: %v", err)
	}
	return id
}

func strptr(s string) *string { return &s }
func intptr(i int) *int       { return &i }

// TestAddInsertsLiveRowWithDefaultsAndAisle: Add inserts a live, household-scoped
// list_items row; quantity defaults to 1 when AddInput.Quantity==0; the aisle
// falls back to catalog.AisleFor(name) when none is supplied.
func TestAddInsertsLiveRowWithDefaultsAndAisle(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	s := listitems.NewStore(pool)
	hh := mkHousehold(t, pool)
	list := mkList(t, pool, hh, false)
	id := "11111111-1111-1111-1111-111111111111"

	// Quantity 0 → defaults to 1; nil aisle → catalog.AisleFor("Mjölk") == 2.
	li, created, err := s.Add(ctx, hh, list, id, listitems.AddInput{Name: "Mjölk", Quantity: 0})
	if err != nil || !created {
		t.Fatalf("add: created=%v err=%v", created, err)
	}
	if li.ID != id || li.ListID != list || li.Name != "Mjölk" {
		t.Fatalf("added row identity = %+v", li)
	}
	if li.Quantity != 1 {
		t.Fatalf("quantity = %d, want default 1", li.Quantity)
	}
	if li.DeletedAt != nil {
		t.Fatalf("added row is soft-deleted: %+v", li)
	}
	want := catalog.AisleFor("Mjölk")
	if want == nil {
		t.Fatal("test precondition: catalog.AisleFor(\"Mjölk\") should map to an aisle")
	}
	if li.Aisle == nil || *li.Aisle != *want {
		t.Fatalf("aisle = %v, want catalog.AisleFor = %d", li.Aisle, *want)
	}

	// The row is live and scoped to the household.
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM list_items
		 WHERE id = $1::uuid AND household_id = $2::uuid AND deleted_at IS NULL`,
		id, hh,
	).Scan(&count); err != nil || count != 1 {
		t.Fatalf("live scoped row count = %d err=%v, want 1", count, err)
	}
}

// TestAddExplicitAisleWins: an explicit AddInput.Aisle overrides the catalog
// fallback, even for a name the catalog would otherwise map elsewhere.
func TestAddExplicitAisleWins(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	s := listitems.NewStore(pool)
	hh := mkHousehold(t, pool)
	list := mkList(t, pool, hh, false)
	id := "22222222-2222-2222-2222-222222222222"

	li, _, err := s.Add(ctx, hh, list, id, listitems.AddInput{Name: "Mjölk", Quantity: 2, Aisle: intptr(9)})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if li.Aisle == nil || *li.Aisle != 9 {
		t.Fatalf("aisle = %v, want explicit 9", li.Aisle)
	}
	if li.Quantity != 2 {
		t.Fatalf("quantity = %d, want 2", li.Quantity)
	}
}

// TestAddBumpsItemsMasterAndReplayDoesNotDoubleBump: adding a row bumps the
// household items master (purchase_count +1, last_purchased_at set); an
// idempotent replay of the same list-item UUID returns created=false and does
// not double-bump.
func TestAddBumpsItemsMasterAndReplayDoesNotDoubleBump(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	s := listitems.NewStore(pool)
	hh := mkHousehold(t, pool)
	list := mkList(t, pool, hh, false)
	id := "33333333-3333-3333-3333-333333333333"

	if _, created, err := s.Add(ctx, hh, list, id, listitems.AddInput{Name: "Mjölk"}); err != nil || !created {
		t.Fatalf("first add: created=%v err=%v", created, err)
	}

	assertMaster := func(where string) {
		t.Helper()
		var rows, purchaseCount int
		var lastPurchased *time.Time
		if err := pool.QueryRow(ctx,
			`SELECT count(*), coalesce(max(purchase_count), 0), max(last_purchased_at)
			   FROM items
			  WHERE household_id = $1::uuid AND lower(name) = lower($2) AND deleted_at IS NULL`,
			hh, "Mjölk",
		).Scan(&rows, &purchaseCount, &lastPurchased); err != nil {
			t.Fatalf("%s: query master: %v", where, err)
		}
		if rows != 1 {
			t.Fatalf("%s: master rows = %d, want exactly 1 for (household, lower(name))", where, rows)
		}
		if purchaseCount != 1 {
			t.Fatalf("%s: purchase_count = %d, want 1", where, purchaseCount)
		}
		if lastPurchased == nil {
			t.Fatalf("%s: last_purchased_at is NULL, want set", where)
		}
	}
	assertMaster("after first add")

	// Idempotent replay of the SAME list-item id must not double-bump.
	if _, created, err := s.Add(ctx, hh, list, id, listitems.AddInput{Name: "Mjölk"}); err != nil || created {
		t.Fatalf("replay: created=%v err=%v, want created=false", created, err)
	}
	assertMaster("after idempotent replay")
}

// TestAddForeignOrDeletedListReturnsNotFound: a list id belonging to another
// household, or one that is soft-deleted, yields ErrNotFound (no existence leak).
func TestAddForeignOrDeletedListReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	s := listitems.NewStore(pool)
	hhA := mkHousehold(t, pool)
	hhB := mkHousehold(t, pool)

	foreignList := mkList(t, pool, hhB, false)
	if _, _, err := s.Add(ctx, hhA, foreignList, "44444444-4444-4444-4444-444444444444",
		listitems.AddInput{Name: "Mjölk"}); !errors.Is(err, listitems.ErrNotFound) {
		t.Fatalf("foreign list err = %v, want ErrNotFound", err)
	}

	deletedList := mkList(t, pool, hhA, true)
	if _, _, err := s.Add(ctx, hhA, deletedList, "55555555-5555-5555-5555-555555555555",
		listitems.AddInput{Name: "Mjölk"}); !errors.Is(err, listitems.ErrNotFound) {
		t.Fatalf("soft-deleted list err = %v, want ErrNotFound", err)
	}
}

// TestAddForeignItemIDReturnsNotFound: an item_id that belongs to another
// household must not be linkable — ErrNotFound (no existence leak).
func TestAddForeignItemIDReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	s := listitems.NewStore(pool)
	hhA := mkHousehold(t, pool)
	hhB := mkHousehold(t, pool)

	list := mkList(t, pool, hhA, false)
	foreignItem := mkItem(t, pool, hhB, "Mjölk")

	if _, _, err := s.Add(ctx, hhA, list, "66666666-6666-6666-6666-666666666666",
		listitems.AddInput{Name: "Mjölk", ItemID: strptr(foreignItem)}); !errors.Is(err, listitems.ErrNotFound) {
		t.Fatalf("foreign item_id err = %v, want ErrNotFound", err)
	}
}
