package lists_test

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
	"github.com/stroem/shopping-list/backend/internal/lists"
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

func strptr(s string) *string { return &s }
func boolptr(b bool) *bool    { return &b }

func TestUpsertCreateThenIdempotentAndGet(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	s := lists.NewStore(pool)
	hh := mkHousehold(t, pool)
	id := "11111111-1111-1111-1111-111111111111"

	// First PUT creates.
	l, created, err := s.Upsert(ctx, hh, id, "Groceries")
	if err != nil || !created {
		t.Fatalf("create: created=%v err=%v", created, err)
	}
	if l.ID != id || l.Name != "Groceries" || l.ArchivedAt != nil || l.DeletedAt != nil {
		t.Fatalf("created list = %+v", l)
	}

	// Replaying the same PUT is idempotent: no new row, created=false.
	l2, created2, err := s.Upsert(ctx, hh, id, "Groceries")
	if err != nil || created2 {
		t.Fatalf("idempotent repeat: created=%v err=%v", created2, err)
	}
	if l2.ID != id {
		t.Fatalf("repeat id = %q", l2.ID)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM lists WHERE id = $1::uuid`, id).Scan(&count); err != nil || count != 1 {
		t.Fatalf("row count = %d err=%v, want 1 (no duplicate)", count, err)
	}

	// Get returns it, scoped to the household.
	g, err := s.Get(ctx, hh, id)
	if err != nil || g.ID != id || g.Name != "Groceries" {
		t.Fatalf("get: %+v err=%v", g, err)
	}

	// Get of a missing id → ErrNotFound.
	if _, err := s.Get(ctx, hh, "22222222-2222-2222-2222-222222222222"); !errors.Is(err, lists.ErrNotFound) {
		t.Fatalf("get missing err = %v, want ErrNotFound", err)
	}
}

func TestListUpdateArchiveSoftDelete(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	s := lists.NewStore(pool)
	hh := mkHousehold(t, pool)
	idA := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	idB := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

	listA, _, err := s.Upsert(ctx, hh, idA, "Groceries")
	if err != nil {
		t.Fatalf("seed A: %v", err)
	}
	before := listA.UpdatedAt
	if _, _, err := s.Upsert(ctx, hh, idB, "Hardware"); err != nil {
		t.Fatalf("seed B: %v", err)
	}

	// List returns both, newest first (B then A).
	all, err := s.List(ctx, hh)
	if err != nil || len(all) != 2 || all[0].ID != idB || all[1].ID != idA {
		t.Fatalf("list = %+v err=%v", all, err)
	}

	// Rename A; sleep first so the clock advances past the seed timestamp.
	time.Sleep(5 * time.Millisecond)
	renamed, err := s.Update(ctx, hh, idA, strptr("Food"), nil)
	if err != nil || renamed.Name != "Food" || renamed.ArchivedAt != nil {
		t.Fatalf("rename: %+v err=%v", renamed, err)
	}
	if !renamed.UpdatedAt.After(before) {
		t.Fatalf("updated_at did not advance: before=%v after=%v", before, renamed.UpdatedAt)
	}

	// Archive A, then unarchive.
	arch, err := s.Update(ctx, hh, idA, nil, boolptr(true))
	if err != nil || arch.ArchivedAt == nil || arch.Name != "Food" {
		t.Fatalf("archive: %+v err=%v", arch, err)
	}
	unarch, err := s.Update(ctx, hh, idA, nil, boolptr(false))
	if err != nil || unarch.ArchivedAt != nil {
		t.Fatalf("unarchive: %+v err=%v", unarch, err)
	}

	// Archived lists are still listed.
	if _, err := s.Update(ctx, hh, idB, nil, boolptr(true)); err != nil {
		t.Fatalf("archive B: %v", err)
	}
	all2, err := s.List(ctx, hh)
	if err != nil || len(all2) != 2 {
		t.Fatalf("list with archived = %+v err=%v", all2, err)
	}

	// Soft-delete A: excluded from List and Get → ErrNotFound.
	if err := s.SoftDelete(ctx, hh, idA); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}
	if _, err := s.Get(ctx, hh, idA); !errors.Is(err, lists.ErrNotFound) {
		t.Fatalf("get deleted err = %v, want ErrNotFound", err)
	}
	all3, err := s.List(ctx, hh)
	if err != nil || len(all3) != 1 || all3[0].ID != idB {
		t.Fatalf("list after delete = %+v err=%v", all3, err)
	}

	// Delete is terminal: re-Upsert of a soft-deleted id → ErrNotFound.
	if _, _, err := s.Upsert(ctx, hh, idA, "Zombie"); !errors.Is(err, lists.ErrNotFound) {
		t.Fatalf("re-upsert deleted err = %v, want ErrNotFound", err)
	}

	// Update / SoftDelete of a missing id → ErrNotFound.
	missing := "cccccccc-cccc-cccc-cccc-cccccccccccc"
	if _, err := s.Update(ctx, hh, missing, strptr("x"), nil); !errors.Is(err, lists.ErrNotFound) {
		t.Fatalf("update missing err = %v, want ErrNotFound", err)
	}
	if err := s.SoftDelete(ctx, hh, missing); !errors.Is(err, lists.ErrNotFound) {
		t.Fatalf("delete missing err = %v, want ErrNotFound", err)
	}
}

func TestCrossHouseholdIsolation(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	s := lists.NewStore(pool)
	hhA := mkHousehold(t, pool)
	hhB := mkHousehold(t, pool)
	id := "dddddddd-dddd-dddd-dddd-dddddddddddd"

	if _, _, err := s.Upsert(ctx, hhA, id, "A's list"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// B cannot see, update, delete, or hijack A's list id.
	if _, err := s.Get(ctx, hhB, id); !errors.Is(err, lists.ErrNotFound) {
		t.Fatalf("B get err = %v, want ErrNotFound", err)
	}
	if _, err := s.Update(ctx, hhB, id, strptr("hijack"), nil); !errors.Is(err, lists.ErrNotFound) {
		t.Fatalf("B update err = %v, want ErrNotFound", err)
	}
	if err := s.SoftDelete(ctx, hhB, id); !errors.Is(err, lists.ErrNotFound) {
		t.Fatalf("B delete err = %v, want ErrNotFound", err)
	}
	if _, _, err := s.Upsert(ctx, hhB, id, "hijack"); !errors.Is(err, lists.ErrNotFound) {
		t.Fatalf("B upsert err = %v, want ErrNotFound", err)
	}
	// A's list is untouched.
	if g, err := s.Get(ctx, hhA, id); err != nil || g.Name != "A's list" {
		t.Fatalf("A's list mutated: %+v err=%v", g, err)
	}
}
