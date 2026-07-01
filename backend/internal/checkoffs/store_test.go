package checkoffs_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/stroem/shopping-list/backend/internal/checkoffs"
	"github.com/stroem/shopping-list/backend/internal/db"
	"github.com/stroem/shopping-list/backend/internal/listitems"
)

// newPool starts a throwaway migrated Postgres via testcontainers and returns a
// pool bound to it. Mirrors the listitems test harness; docker is available here
// so these tests run rather than skip.
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

// mkHousehold inserts a household and returns its id.
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

// mkUser inserts a users row (checked_by/user_id FK target) with a unique
// device_id under householdID and returns its id.
func mkUser(t *testing.T, pool *pgxpool.Pool, householdID, deviceID string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (device_id, household_id) VALUES ($1, $2::uuid) RETURNING id::text`,
		deviceID, householdID,
	).Scan(&id); err != nil {
		t.Fatalf("mkUser: %v", err)
	}
	return id
}

// mkList inserts a live list under householdID and returns its id.
func mkList(t *testing.T, pool *pgxpool.Pool, householdID string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO lists (household_id, name) VALUES ($1::uuid, 'Groceries') RETURNING id::text`,
		householdID,
	).Scan(&id); err != nil {
		t.Fatalf("mkList: %v", err)
	}
	return id
}

// mkItem inserts an items-master row under householdID and returns its id.
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

// seedLinkedListItem creates an items-master row and a live list item (via the
// tested listitems.Add path) linked to it, returning the item id and list item.
// The list item carries the given name and quantity so the check-off event's
// item_id/quantity are observable.
func seedLinkedListItem(t *testing.T, pool *pgxpool.Pool, hh, list, listItemID, name string, quantity int) (itemID string, li listitems.ListItem) {
	t.Helper()
	itemID = mkItem(t, pool, hh, name)
	s := listitems.NewStore(pool)
	li, _, err := s.Add(context.Background(), hh, list, listItemID,
		listitems.AddInput{Name: name, Quantity: quantity, ItemID: strptr(itemID)})
	if err != nil {
		t.Fatalf("seed list item %s: %v", listItemID, err)
	}
	if li.ItemID == nil || *li.ItemID != itemID {
		t.Fatalf("seed precondition: list item not linked to item %s: %+v", itemID, li)
	}
	return itemID, li
}

// countEventsForListItem returns how many check_off_events rows exist for a
// list_item_id.
func countEventsForListItem(t *testing.T, pool *pgxpool.Pool, listItemID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM check_off_events WHERE list_item_id = $1::uuid`,
		listItemID,
	).Scan(&n); err != nil {
		t.Fatalf("count events: %v", err)
	}
	return n
}

// itemMaster reads purchase_count and last_purchased_at for an items row by id.
func itemMaster(t *testing.T, pool *pgxpool.Pool, itemID string) (purchaseCount int, lastPurchased *time.Time) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT purchase_count, last_purchased_at FROM items WHERE id = $1::uuid`,
		itemID,
	).Scan(&purchaseCount, &lastPurchased); err != nil {
		t.Fatalf("read items master: %v", err)
	}
	return purchaseCount, lastPurchased
}

// TestCheckOffMarksListItemAndRecordsOneEvent: checking off a live list item
// stamps checked_at/checked_by on the returned row and writes exactly one
// check_off_events row carrying the household, user, linked item, and quantity.
func TestCheckOffMarksListItemAndRecordsOneEvent(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	s := checkoffs.NewStore(pool)
	hh := mkHousehold(t, pool)
	user := mkUser(t, pool, hh, "device-a")
	list := mkList(t, pool, hh)
	id := "11111111-1111-1111-1111-111111111111"
	itemID, _ := seedLinkedListItem(t, pool, hh, list, id, "Mjölk", 3)

	li, err := s.CheckOff(ctx, hh, id, user, nil)
	if err != nil {
		t.Fatalf("check off: %v", err)
	}
	if li.CheckedAt == nil {
		t.Fatalf("CheckedAt = nil, want set")
	}
	if li.CheckedBy == nil || *li.CheckedBy != user {
		t.Fatalf("CheckedBy = %v, want %s", li.CheckedBy, user)
	}

	if n := countEventsForListItem(t, pool, id); n != 1 {
		t.Fatalf("check_off_events rows = %d, want exactly 1", n)
	}

	var (
		evHousehold, evUser, evItem string
		evQuantity                  int
	)
	if err := pool.QueryRow(ctx,
		`SELECT household_id::text, user_id::text, item_id::text, quantity
		   FROM check_off_events WHERE list_item_id = $1::uuid`,
		id,
	).Scan(&evHousehold, &evUser, &evItem, &evQuantity); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if evHousehold != hh {
		t.Fatalf("event household_id = %s, want %s", evHousehold, hh)
	}
	if evUser != user {
		t.Fatalf("event user_id = %s, want %s", evUser, user)
	}
	if evItem != itemID {
		t.Fatalf("event item_id = %s, want linked item %s", evItem, itemID)
	}
	if evQuantity != 3 {
		t.Fatalf("event quantity = %d, want 3 (the list item's quantity)", evQuantity)
	}
}

// TestCheckOffBumpsItemsMaster: a single check-off increments the linked item's
// purchase_count by one and advances last_purchased_at to ~now.
func TestCheckOffBumpsItemsMaster(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	s := checkoffs.NewStore(pool)
	hh := mkHousehold(t, pool)
	user := mkUser(t, pool, hh, "device-b")
	list := mkList(t, pool, hh)
	id := "22222222-2222-2222-2222-222222222222"
	itemID, _ := seedLinkedListItem(t, pool, hh, list, id, "Mjölk", 1)

	before, _ := itemMaster(t, pool, itemID)

	start := time.Now()
	if _, err := s.CheckOff(ctx, hh, id, user, nil); err != nil {
		t.Fatalf("check off: %v", err)
	}

	after, lastPurchased := itemMaster(t, pool, itemID)
	if after != before+1 {
		t.Fatalf("purchase_count = %d, want before+1 = %d", after, before+1)
	}
	if lastPurchased == nil {
		t.Fatalf("last_purchased_at = nil, want set to ~now")
	}
	if lastPurchased.Before(start.Add(-time.Minute)) {
		t.Fatalf("last_purchased_at = %v, want ~now (>= %v)", lastPurchased, start)
	}
}

// TestCheckOffReplayWithClientEventIDIsIdempotent: two check-offs carrying the
// same non-nil client_event_id write only one event and bump the master only once.
func TestCheckOffReplayWithClientEventIDIsIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	s := checkoffs.NewStore(pool)
	hh := mkHousehold(t, pool)
	user := mkUser(t, pool, hh, "device-c")
	list := mkList(t, pool, hh)
	id := "33333333-3333-3333-3333-333333333333"
	itemID, _ := seedLinkedListItem(t, pool, hh, list, id, "Mjölk", 1)

	before, _ := itemMaster(t, pool, itemID)
	clientEventID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"

	if _, err := s.CheckOff(ctx, hh, id, user, strptr(clientEventID)); err != nil {
		t.Fatalf("first check off: %v", err)
	}
	if _, err := s.CheckOff(ctx, hh, id, user, strptr(clientEventID)); err != nil {
		t.Fatalf("replay check off: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM check_off_events WHERE client_event_id = $1::uuid`,
		clientEventID,
	).Scan(&n); err != nil {
		t.Fatalf("count events by client_event_id: %v", err)
	}
	if n != 1 {
		t.Fatalf("check_off_events for client_event_id = %d, want exactly 1 (idempotent replay)", n)
	}

	after, _ := itemMaster(t, pool, itemID)
	if after != before+1 {
		t.Fatalf("purchase_count = %d, want before+1 = %d (bumped once, not twice)", after, before+1)
	}
}

// TestCheckOffWithoutClientEventIDAppends: absent a client_event_id every call
// appends, so two check-offs of the same list item yield two events.
func TestCheckOffWithoutClientEventIDAppends(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	s := checkoffs.NewStore(pool)
	hh := mkHousehold(t, pool)
	user := mkUser(t, pool, hh, "device-d")
	list := mkList(t, pool, hh)
	id := "44444444-4444-4444-4444-444444444444"
	seedLinkedListItem(t, pool, hh, list, id, "Mjölk", 1)

	if _, err := s.CheckOff(ctx, hh, id, user, nil); err != nil {
		t.Fatalf("first check off: %v", err)
	}
	if _, err := s.CheckOff(ctx, hh, id, user, nil); err != nil {
		t.Fatalf("second check off: %v", err)
	}

	if n := countEventsForListItem(t, pool, id); n != 2 {
		t.Fatalf("check_off_events rows = %d, want 2 (append-only, no dedup)", n)
	}
}

// TestCheckOffMissingOrForeignListItemReturnsNotFound: a random id, or an id
// belonging to another household, yields ErrNotFound (no existence leak).
func TestCheckOffMissingOrForeignListItemReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	s := checkoffs.NewStore(pool)
	hhA := mkHousehold(t, pool)
	hhB := mkHousehold(t, pool)
	userA := mkUser(t, pool, hhA, "device-e")

	// Random, non-existent id.
	if _, err := s.CheckOff(ctx, hhA, "99999999-9999-9999-9999-999999999999", userA, nil); !errors.Is(err, checkoffs.ErrNotFound) {
		t.Fatalf("missing list item err = %v, want ErrNotFound", err)
	}

	// A live list item that belongs to hhB must be invisible to hhA.
	listB := mkList(t, pool, hhB)
	foreignID := "55555555-5555-5555-5555-555555555555"
	seedLinkedListItem(t, pool, hhB, listB, foreignID, "Mjölk", 1)

	if _, err := s.CheckOff(ctx, hhA, foreignID, userA, nil); !errors.Is(err, checkoffs.ErrNotFound) {
		t.Fatalf("foreign list item err = %v, want ErrNotFound", err)
	}
	// And nothing was written for the foreign row.
	if n := countEventsForListItem(t, pool, foreignID); n != 0 {
		t.Fatalf("check_off_events for foreign list item = %d, want 0", n)
	}
}
