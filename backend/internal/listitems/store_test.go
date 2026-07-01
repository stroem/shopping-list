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

// mustAdd seeds a live list item via the existing Add path and returns the row;
// it fails the test on any error so callers can focus on the behaviour under test.
func mustAdd(t *testing.T, s *listitems.Store, hh, list, id string, in listitems.AddInput) listitems.ListItem {
	t.Helper()
	li, _, err := s.Add(context.Background(), hh, list, id, in)
	if err != nil {
		t.Fatalf("seed add %s: %v", id, err)
	}
	return li
}

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

// TestListOrdersByPositionThenCreated: List returns only the target list's live
// rows, ordered by position ASC then created_at ASC. Positions are set via Update
// so two rows share a position and the created_at tiebreak becomes observable —
// the returned order deliberately differs from insertion order.
func TestListOrdersByPositionThenCreated(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	s := listitems.NewStore(pool)
	hh := mkHousehold(t, pool)
	list := mkList(t, pool, hh, false)
	other := mkList(t, pool, hh, false)

	idA := "a0000000-0000-0000-0000-000000000000"
	idB := "b0000000-0000-0000-0000-000000000000"
	idC := "c0000000-0000-0000-0000-000000000000"

	// Insertion order A, B, C with distinct created_at (sleeps make the tiebreak
	// deterministic); all land at position 0 initially.
	mustAdd(t, s, hh, list, idA, listitems.AddInput{Name: "Ägg"})
	time.Sleep(2 * time.Millisecond)
	mustAdd(t, s, hh, list, idB, listitems.AddInput{Name: "Bröd"})
	time.Sleep(2 * time.Millisecond)
	mustAdd(t, s, hh, list, idC, listitems.AddInput{Name: "Citron"})

	// A row on a different list of the SAME household must not leak in.
	mustAdd(t, s, hh, other, "d0000000-0000-0000-0000-000000000000",
		listitems.AddInput{Name: "Dill"})

	// Reorder: A and B share position 5, C is ahead at position 1.
	// Expected order by (position, created_at): C(1), then A(5, older), then B(5).
	if _, err := s.Update(ctx, hh, idA, nil, nil, intptr(5)); err != nil {
		t.Fatalf("reorder A: %v", err)
	}
	if _, err := s.Update(ctx, hh, idB, nil, nil, intptr(5)); err != nil {
		t.Fatalf("reorder B: %v", err)
	}
	if _, err := s.Update(ctx, hh, idC, nil, nil, intptr(1)); err != nil {
		t.Fatalf("reorder C: %v", err)
	}

	got, err := s.List(ctx, hh, list)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	gotIDs := make([]string, len(got))
	for i, li := range got {
		gotIDs[i] = li.ID
	}
	want := []string{idC, idA, idB}
	if len(gotIDs) != len(want) {
		t.Fatalf("list len = %d (%v), want %d (%v)", len(gotIDs), gotIDs, len(want), want)
	}
	for i := range want {
		if gotIDs[i] != want[i] {
			t.Fatalf("list order = %v, want %v (position ASC then created_at ASC)", gotIDs, want)
		}
	}
}

// TestListItemCrossHouseholdIsolation: List/Update/SoftDelete are household-scoped.
// A caller in another household sees an empty List for a foreign list and cannot
// edit or remove a foreign row — ErrNotFound, no existence leak.
func TestListItemCrossHouseholdIsolation(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	s := listitems.NewStore(pool)
	hhA := mkHousehold(t, pool)
	hhB := mkHousehold(t, pool)
	listA := mkList(t, pool, hhA, false)
	id := "e0000000-0000-0000-0000-000000000000"
	mustAdd(t, s, hhA, listA, id, listitems.AddInput{Name: "Mjölk"})

	// hhB listing hhA's list leaks nothing.
	got, err := s.List(ctx, hhB, listA)
	if err != nil {
		t.Fatalf("cross-household list err = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("cross-household list = %v, want empty", got)
	}

	// hhB cannot edit or remove hhA's row.
	if _, err := s.Update(ctx, hhB, id, intptr(3), nil, nil); !errors.Is(err, listitems.ErrNotFound) {
		t.Fatalf("cross-household update err = %v, want ErrNotFound", err)
	}
	if err := s.SoftDelete(ctx, hhB, id); !errors.Is(err, listitems.ErrNotFound) {
		t.Fatalf("cross-household soft-delete err = %v, want ErrNotFound", err)
	}
}

// TestUpdatePartialFieldsAndTimestamp: Update touches only the fields supplied —
// a nil field is left unchanged — and every call advances updated_at. An absent
// or soft-deleted id yields ErrNotFound.
func TestUpdatePartialFieldsAndTimestamp(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	s := listitems.NewStore(pool)
	hh := mkHousehold(t, pool)
	list := mkList(t, pool, hh, false)
	id := "f0000000-0000-0000-0000-000000000000"

	seeded := mustAdd(t, s, hh, list, id,
		listitems.AddInput{Name: "Mjölk", Quantity: 2, Note: strptr("skummjölk")})
	if seeded.Quantity != 2 || seeded.Note == nil || *seeded.Note != "skummjölk" || seeded.Position != 0 {
		t.Fatalf("seed row = %+v, want quantity 2, note skummjölk, position 0", seeded)
	}
	before := seeded.UpdatedAt

	// Quantity only: note and position must be untouched.
	time.Sleep(5 * time.Millisecond)
	q, err := s.Update(ctx, hh, id, intptr(7), nil, nil)
	if err != nil {
		t.Fatalf("update quantity: %v", err)
	}
	if q.Quantity != 7 {
		t.Fatalf("quantity = %d, want 7", q.Quantity)
	}
	if q.Note == nil || *q.Note != "skummjölk" {
		t.Fatalf("note = %v, want unchanged skummjölk", q.Note)
	}
	if q.Position != 0 {
		t.Fatalf("position = %d, want unchanged 0", q.Position)
	}
	if !q.UpdatedAt.After(before) {
		t.Fatalf("updated_at did not advance: before=%v after=%v", before, q.UpdatedAt)
	}

	// Note only: quantity and position must be untouched.
	n, err := s.Update(ctx, hh, id, nil, strptr("laktosfri"), nil)
	if err != nil {
		t.Fatalf("update note: %v", err)
	}
	if n.Note == nil || *n.Note != "laktosfri" {
		t.Fatalf("note = %v, want laktosfri", n.Note)
	}
	if n.Quantity != 7 {
		t.Fatalf("quantity = %d, want unchanged 7", n.Quantity)
	}
	if n.Position != 0 {
		t.Fatalf("position = %d, want unchanged 0", n.Position)
	}

	// Position only (reorder): quantity and note must be untouched.
	p, err := s.Update(ctx, hh, id, nil, nil, intptr(4))
	if err != nil {
		t.Fatalf("update position: %v", err)
	}
	if p.Position != 4 {
		t.Fatalf("position = %d, want 4", p.Position)
	}
	if p.Quantity != 7 {
		t.Fatalf("quantity = %d, want unchanged 7", p.Quantity)
	}
	if p.Note == nil || *p.Note != "laktosfri" {
		t.Fatalf("note = %v, want unchanged laktosfri", p.Note)
	}

	// Absent id → ErrNotFound.
	if _, err := s.Update(ctx, hh, "99999999-9999-9999-9999-999999999999", intptr(1), nil, nil); !errors.Is(err, listitems.ErrNotFound) {
		t.Fatalf("update absent id err = %v, want ErrNotFound", err)
	}

	// Soft-deleted id → ErrNotFound.
	if err := s.SoftDelete(ctx, hh, id); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}
	if _, err := s.Update(ctx, hh, id, intptr(1), nil, nil); !errors.Is(err, listitems.ErrNotFound) {
		t.Fatalf("update soft-deleted id err = %v, want ErrNotFound", err)
	}
}

// TestSoftDeleteExcludesAndIsTerminal: after SoftDelete the row is gone from List,
// a later Update → ErrNotFound, and re-Add of the same id → ErrNotFound (delete is
// terminal, mirroring lists). SoftDelete of an absent id → ErrNotFound.
func TestSoftDeleteExcludesAndIsTerminal(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	s := listitems.NewStore(pool)
	hh := mkHousehold(t, pool)
	list := mkList(t, pool, hh, false)
	id := "12121212-1212-1212-1212-121212121212"
	mustAdd(t, s, hh, list, id, listitems.AddInput{Name: "Mjölk"})

	if err := s.SoftDelete(ctx, hh, id); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}

	// Excluded from List.
	got, err := s.List(ctx, hh, list)
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("list after delete = %v, want empty", got)
	}

	// A later Update misses the live scope.
	if _, err := s.Update(ctx, hh, id, intptr(1), nil, nil); !errors.Is(err, listitems.ErrNotFound) {
		t.Fatalf("update after delete err = %v, want ErrNotFound", err)
	}

	// Delete is terminal: re-Add of the same id must not resurrect the row.
	if _, _, err := s.Add(ctx, hh, list, id, listitems.AddInput{Name: "Mjölk"}); !errors.Is(err, listitems.ErrNotFound) {
		t.Fatalf("re-add of soft-deleted id err = %v, want ErrNotFound (terminal)", err)
	}

	// SoftDelete of an absent id → ErrNotFound.
	if err := s.SoftDelete(ctx, hh, "34343434-3434-3434-3434-343434343434"); !errors.Is(err, listitems.ErrNotFound) {
		t.Fatalf("soft-delete absent id err = %v, want ErrNotFound", err)
	}
}
