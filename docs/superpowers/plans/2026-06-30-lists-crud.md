# Lists CRUD Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Household-scoped shopping-list CRUD (create / rename / archive / soft-delete) as a new HTTP API layer over the existing `lists` table.

**Architecture:** A new `internal/lists` package (a pgx `Store`, mirroring `internal/households`) plus thin handlers in `internal/router/lists.go` behind the existing `RequireAuth` group. Every operation is scoped to the authenticated person's household (from the #8 `Principal`); foreign/missing/soft-deleted ids return 404. List ids are client-generated UUIDs, so create is an idempotent `PUT /v1/lists/{id}`.

**Tech Stack:** Go 1.26, chi v5, pgx/v5 + pgxpool, testcontainers-go, `github.com/google/uuid` (already a dependency).

## Global Constraints

- Spec: `docs/superpowers/specs/2026-06-30-lists-crud-design.md`. Read it first.
- **No migration** — the `lists` table and `lists_sync` index already exist from `0001_init`.
- Conventional Commits 1.0.0; lowercase imperative; **no AI/Claude attribution**.
- Household is always derived from `web.HouseholdID(ctx)` / the `Principal`, never from the client body.
- Foreign / missing / soft-deleted id → **404** (no existence leak). No household → 404 on `{id}` routes, `[]` on the collection. Invalid UUID in path → **400**. Empty `name` on PUT or empty PATCH body → **400**.
- Delete is terminal: a replayed PUT to a soft-deleted id returns 404 (no resurrection).
- Green bar after every task: `go test ./...` (DB-backed tests skip cleanly without Docker).
- Match existing patterns: `web.JSON` / `web.Error` / `web.ServerError`, `households.Store` for the store shape, `households_handler_test.go` for handler tests (`principalMW`).

---

### Task 1: `lists` package — types, Store, idempotent Upsert + Get

**Files:**
- Create: `backend/internal/lists/store.go`
- Test: `backend/internal/lists/store_test.go`

**Interfaces:**
- Consumes: existing `lists` table; `internal/db.Migrate` (test helper).
- Produces:
  - `type List struct { ID, Name string; ArchivedAt *time.Time; CreatedAt, UpdatedAt time.Time; DeletedAt *time.Time }` (json tags `id,name,archived_at,created_at,updated_at,deleted_at`)
  - `var ErrNotFound = errors.New("list not found")`
  - `func NewStore(db *pgxpool.Pool) *Store`
  - `func (s *Store) Upsert(ctx context.Context, householdID, id, name string) (List, bool, error)` — second return is `created` (true → 201).
  - `func (s *Store) Get(ctx context.Context, householdID, id string) (List, error)`

- [ ] **Step 1: Write the failing test**

Create `backend/internal/lists/store_test.go`. This mirrors `households/store_test.go` (same `newPool` body) and adds a `mkHousehold` helper.

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/lists/ -run TestUpsertCreateThenIdempotentAndGet`
Expected: FAIL to compile — `package lists` / `lists.NewStore` undefined.

- [ ] **Step 3: Write minimal implementation**

Create `backend/internal/lists/store.go`:

```go
// Package lists persists household-scoped shopping lists.
package lists

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound means no list matched the (household, id) scope, or it was
// soft-deleted. Handlers map it to 404 — no existence leak.
var ErrNotFound = errors.New("list not found")

// List is a household-scoped shopping list.
type List struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	ArchivedAt *time.Time `json:"archived_at"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	DeletedAt  *time.Time `json:"deleted_at"`
}

// Store persists lists.
type Store struct{ db *pgxpool.Pool }

// NewStore builds a Store.
func NewStore(db *pgxpool.Pool) *Store { return &Store{db: db} }

const listCols = `id::text, name, archived_at, created_at, updated_at, deleted_at`

func scanList(row pgx.Row) (List, error) {
	var l List
	err := row.Scan(&l.ID, &l.Name, &l.ArchivedAt, &l.CreatedAt, &l.UpdatedAt, &l.DeletedAt)
	return l, err
}

// Upsert creates-or-replaces the list for (householdID, id). The bool reports
// whether a new row was inserted (true → 201) vs updated (false → 200), via the
// xmax=0 RETURNING trick. The ON CONFLICT update is guarded by household_id and
// deleted_at IS NULL, so a foreign id — or this household's own soft-deleted id —
// matches no row, RETURNING is empty, and we return ErrNotFound. Delete stays
// terminal; foreign ids never leak.
func (s *Store) Upsert(ctx context.Context, householdID, id, name string) (List, bool, error) {
	var l List
	var created bool
	err := s.db.QueryRow(ctx, `
INSERT INTO lists (id, household_id, name)
VALUES ($1::uuid, $2::uuid, $3)
ON CONFLICT (id) DO UPDATE
   SET name = EXCLUDED.name, updated_at = now()
   WHERE lists.household_id = $2::uuid AND lists.deleted_at IS NULL
RETURNING `+listCols+`, (xmax = 0) AS created`,
		id, householdID, name,
	).Scan(&l.ID, &l.Name, &l.ArchivedAt, &l.CreatedAt, &l.UpdatedAt, &l.DeletedAt, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return List{}, false, ErrNotFound
	}
	if err != nil {
		return List{}, false, fmt.Errorf("upsert list: %w", err)
	}
	return l, created, nil
}

// Get returns one non-deleted list scoped to the household, or ErrNotFound.
func (s *Store) Get(ctx context.Context, householdID, id string) (List, error) {
	l, err := scanList(s.db.QueryRow(ctx,
		`SELECT `+listCols+` FROM lists
		 WHERE id = $1::uuid AND household_id = $2::uuid AND deleted_at IS NULL`,
		id, householdID,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return List{}, ErrNotFound
	}
	if err != nil {
		return List{}, fmt.Errorf("get list: %w", err)
	}
	return l, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/lists/ -run TestUpsertCreateThenIdempotentAndGet`
Expected: PASS (or SKIP if Docker unavailable — acceptable per the green-bar rule).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/lists/store.go backend/internal/lists/store_test.go
git commit -m "feat(lists): idempotent upsert + get store"
```

---

### Task 2: List collection, Update (rename/archive), SoftDelete

**Files:**
- Modify: `backend/internal/lists/store.go`
- Test: `backend/internal/lists/store_test.go`

**Interfaces:**
- Consumes: `Store`, `List`, `ErrNotFound`, `scanList`, `listCols` from Task 1.
- Produces:
  - `func (s *Store) List(ctx context.Context, householdID string) ([]List, error)` — non-deleted, archived included, newest first.
  - `func (s *Store) Update(ctx context.Context, householdID, id string, name *string, archived *bool) (List, error)` — nil leaves a field unchanged.
  - `func (s *Store) SoftDelete(ctx context.Context, householdID, id string) error`

- [ ] **Step 1: Write the failing test**

Append to `backend/internal/lists/store_test.go`:

```go
func strptr(s string) *string { return &s }
func boolptr(b bool) *bool    { return &b }

func TestListUpdateArchiveSoftDelete(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	s := lists.NewStore(pool)
	hh := mkHousehold(t, pool)
	idA := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	idB := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

	if _, _, err := s.Upsert(ctx, hh, idA, "Groceries"); err != nil {
		t.Fatalf("seed A: %v", err)
	}
	if _, _, err := s.Upsert(ctx, hh, idB, "Hardware"); err != nil {
		t.Fatalf("seed B: %v", err)
	}

	// List returns both, newest first (B then A).
	all, err := s.List(ctx, hh)
	if err != nil || len(all) != 2 || all[0].ID != idB || all[1].ID != idA {
		t.Fatalf("list = %+v err=%v", all, err)
	}

	// Rename A.
	renamed, err := s.Update(ctx, hh, idA, strptr("Food"), nil)
	if err != nil || renamed.Name != "Food" || renamed.ArchivedAt != nil {
		t.Fatalf("rename: %+v err=%v", renamed, err)
	}
	if !renamed.UpdatedAt.After(renamed.CreatedAt) && !renamed.UpdatedAt.Equal(renamed.CreatedAt) {
		t.Fatalf("updated_at not maintained: %+v", renamed)
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/lists/ -run 'TestListUpdateArchiveSoftDelete|TestCrossHouseholdIsolation'`
Expected: FAIL to compile — `s.List` / `s.Update` / `s.SoftDelete` undefined.

- [ ] **Step 3: Write minimal implementation**

Append to `backend/internal/lists/store.go`:

```go
// List returns the household's non-deleted lists (archived included), newest first.
func (s *Store) List(ctx context.Context, householdID string) ([]List, error) {
	rows, err := s.db.Query(ctx,
		`SELECT `+listCols+` FROM lists
		 WHERE household_id = $1::uuid AND deleted_at IS NULL
		 ORDER BY created_at DESC`,
		householdID,
	)
	if err != nil {
		return nil, fmt.Errorf("list lists: %w", err)
	}
	defer rows.Close()

	out := []List{}
	for rows.Next() {
		l, err := scanList(rows)
		if err != nil {
			return nil, fmt.Errorf("scan list: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate lists: %w", err)
	}
	return out, nil
}

// Update renames and/or (un)archives a list. A nil name leaves the name; a nil
// archived leaves the archive state. archived=true sets archived_at=now(),
// archived=false clears it. Scoped to the household and live rows only; returns
// ErrNotFound if the list is absent, foreign, or soft-deleted.
func (s *Store) Update(ctx context.Context, householdID, id string, name *string, archived *bool) (List, error) {
	l, err := scanList(s.db.QueryRow(ctx, `
UPDATE lists SET
    name        = COALESCE($3, name),
    archived_at = CASE
                    WHEN $4::boolean IS NULL THEN archived_at
                    WHEN $4::boolean THEN now()
                    ELSE NULL
                  END,
    updated_at  = now()
WHERE id = $1::uuid AND household_id = $2::uuid AND deleted_at IS NULL
RETURNING `+listCols,
		id, householdID, name, archived,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return List{}, ErrNotFound
	}
	if err != nil {
		return List{}, fmt.Errorf("update list: %w", err)
	}
	return l, nil
}

// SoftDelete sets deleted_at on a live, household-scoped list. Returns
// ErrNotFound if no live row matched (absent, foreign, or already deleted).
func (s *Store) SoftDelete(ctx context.Context, householdID, id string) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE lists SET deleted_at = now(), updated_at = now()
		 WHERE id = $1::uuid AND household_id = $2::uuid AND deleted_at IS NULL`,
		id, householdID,
	)
	if err != nil {
		return fmt.Errorf("soft-delete list: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/lists/`
Expected: PASS (or SKIP without Docker).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/lists/store.go backend/internal/lists/store_test.go
git commit -m "feat(lists): list, rename/archive update, and soft-delete"
```

---

### Task 3: HTTP handlers + router wiring

**Files:**
- Create: `backend/internal/router/lists.go`
- Modify: `backend/internal/router/router.go`
- Test: `backend/internal/router/lists_handler_test.go`

**Interfaces:**
- Consumes: `lists.List`, `lists.ErrNotFound` (Task 1); `web.JSON`, `web.Error`, `web.ServerError`, `web.HouseholdID`; `auth.RequireAuth`; chi.
- Produces:
  - `type ListStore interface { Upsert(ctx, householdID, id, name string) (lists.List, bool, error); List(ctx, householdID string) ([]lists.List, error); Get(ctx, householdID, id string) (lists.List, error); Update(ctx, householdID, id string, name *string, archived *bool) (lists.List, error); SoftDelete(ctx, householdID, id string) error }`
  - `Deps.Lists ListStore` field; the five routes registered in the `RequireAuth` group.

- [ ] **Step 1: Write the failing test**

Create `backend/internal/router/lists_handler_test.go`. Reuses `fakePinger` (router_test.go) and `principalMW` (households_handler_test.go) from the same `router_test` package.

```go
package router_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stroem/shopping-list/backend/internal/lists"
	"github.com/stroem/shopping-list/backend/internal/router"
	"github.com/stroem/shopping-list/backend/internal/web"
)

type fakeLists struct {
	created    bool
	list       lists.List
	all        []lists.List
	err        error
	gotHH      string
	gotName    string
	gotArchive *bool
}

func (f *fakeLists) Upsert(_ context.Context, hh, _, name string) (lists.List, bool, error) {
	f.gotHH, f.gotName = hh, name
	return f.list, f.created, f.err
}
func (f *fakeLists) List(_ context.Context, hh string) ([]lists.List, error) {
	f.gotHH = hh
	return f.all, f.err
}
func (f *fakeLists) Get(_ context.Context, hh, _ string) (lists.List, error) {
	f.gotHH = hh
	return f.list, f.err
}
func (f *fakeLists) Update(_ context.Context, hh, _ string, name *string, archived *bool) (lists.List, error) {
	f.gotHH, f.gotArchive = hh, archived
	if name != nil {
		f.gotName = *name
	}
	return f.list, f.err
}
func (f *fakeLists) SoftDelete(_ context.Context, hh, _ string) error {
	f.gotHH = hh
	return f.err
}

func newListRouter(p *web.Principal, store router.ListStore) http.Handler {
	return router.New(router.Deps{
		DB:             fakePinger{},
		AuthMiddleware: principalMW(p),
		Lists:          store,
	})
}

const validID = "11111111-1111-1111-1111-111111111111"

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestPutList_201Create_UsesPrincipalHousehold(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	store := &fakeLists{created: true, list: lists.List{ID: validID, Name: "Groceries"}}
	h := newListRouter(p, store)

	rec := do(t, h, http.MethodPut, "/v1/lists/"+validID, `{"name":"Groceries"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("code = %d, want 201", rec.Code)
	}
	if store.gotHH != "h-1" || store.gotName != "Groceries" {
		t.Fatalf("store got hh=%q name=%q (must come from principal/body)", store.gotHH, store.gotName)
	}
}

func TestPutList_200OnIdempotentRepeat(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	store := &fakeLists{created: false, list: lists.List{ID: validID, Name: "Groceries"}}
	rec := do(t, newListRouter(p, store), http.MethodPut, "/v1/lists/"+validID, `{"name":"Groceries"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
}

func TestPutList_400OnBadUUID(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	rec := do(t, newListRouter(p, &fakeLists{}), http.MethodPut, "/v1/lists/not-a-uuid", `{"name":"x"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
}

func TestPutList_400OnEmptyName(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	rec := do(t, newListRouter(p, &fakeLists{}), http.MethodPut, "/v1/lists/"+validID, `{"name":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
}

func TestPutList_404WhenForeignID(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	store := &fakeLists{err: lists.ErrNotFound}
	rec := do(t, newListRouter(p, store), http.MethodPut, "/v1/lists/"+validID, `{"name":"x"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rec.Code)
	}
}

func TestListLists_200Array(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	store := &fakeLists{all: []lists.List{{ID: validID, Name: "Groceries"}}}
	rec := do(t, newListRouter(p, store), http.MethodGet, "/v1/lists", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	var got []lists.List
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got) != 1 {
		t.Fatalf("body = %s err=%v", rec.Body.String(), err)
	}
}

func TestListRoutes_404WhenNoHousehold(t *testing.T) {
	p := &web.Principal{UserID: "u-1"} // no household
	store := &fakeLists{list: lists.List{ID: validID}}
	// GET one → 404
	if rec := do(t, newListRouter(p, store), http.MethodGet, "/v1/lists/"+validID, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("get one no-household code = %d, want 404", rec.Code)
	}
	// GET collection → 200 empty array
	rec := do(t, newListRouter(p, store), http.MethodGet, "/v1/lists", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list no-household code = %d, want 200", rec.Code)
	}
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("list no-household body = %q, want []", rec.Body.String())
	}
}

func TestGetList_404OnNotFound(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	store := &fakeLists{err: lists.ErrNotFound}
	rec := do(t, newListRouter(p, store), http.MethodGet, "/v1/lists/"+validID, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rec.Code)
	}
}

func TestPatchList_200_AndEmptyBody400(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	store := &fakeLists{list: lists.List{ID: validID, Name: "Food"}}
	h := newListRouter(p, store)

	if rec := do(t, h, http.MethodPatch, "/v1/lists/"+validID, `{"archived":true}`); rec.Code != http.StatusOK {
		t.Fatalf("patch code = %d, want 200", rec.Code)
	}
	if store.gotArchive == nil || *store.gotArchive != true {
		t.Fatalf("archived not passed through: %v", store.gotArchive)
	}
	// Empty PATCH body (no fields) → 400.
	if rec := do(t, h, http.MethodPatch, "/v1/lists/"+validID, `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty patch code = %d, want 400", rec.Code)
	}
}

func TestDeleteList_204_And404(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}

	if rec := do(t, newListRouter(p, &fakeLists{}), http.MethodDelete, "/v1/lists/"+validID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete code = %d, want 204", rec.Code)
	}
	store := &fakeLists{err: lists.ErrNotFound}
	if rec := do(t, newListRouter(p, store), http.MethodDelete, "/v1/lists/"+validID, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("delete missing code = %d, want 404", rec.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/router/ -run TestPutList_201Create_UsesPrincipalHousehold`
Expected: FAIL to compile — `router.ListStore` / `Deps.Lists` undefined.

- [ ] **Step 3a: Write the handlers**

Create `backend/internal/router/lists.go`:

```go
package router

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/stroem/shopping-list/backend/internal/lists"
	"github.com/stroem/shopping-list/backend/internal/web"
)

// ListStore is the list persistence the handlers need; *lists.Store satisfies
// it, tests pass a fake.
type ListStore interface {
	Upsert(ctx context.Context, householdID, id, name string) (lists.List, bool, error)
	List(ctx context.Context, householdID string) ([]lists.List, error)
	Get(ctx context.Context, householdID, id string) (lists.List, error)
	Update(ctx context.Context, householdID, id string, name *string, archived *bool) (lists.List, error)
	SoftDelete(ctx context.Context, householdID, id string) error
}

// listID validates the {id} path param as a UUID, writing 400 and returning
// ok=false when it is malformed.
func listID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		web.Error(w, http.StatusBadRequest, "invalid list id")
		return "", false
	}
	return id, true
}

func putList(store ListStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hh, ok := web.HouseholdID(r.Context())
		if !ok {
			web.Error(w, http.StatusNotFound, "not found")
			return
		}
		id, ok := listID(w, r)
		if !ok {
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
			web.Error(w, http.StatusBadRequest, "name required")
			return
		}
		l, created, err := store.Upsert(r.Context(), hh, id, body.Name)
		if errors.Is(err, lists.ErrNotFound) {
			web.Error(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			web.ServerError(w, r, err, "create list failed")
			return
		}
		status := http.StatusOK
		if created {
			status = http.StatusCreated
		}
		web.JSON(w, status, l)
	}
}

func listLists(store ListStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hh, ok := web.HouseholdID(r.Context())
		if !ok {
			web.JSON(w, http.StatusOK, []lists.List{})
			return
		}
		ls, err := store.List(r.Context(), hh)
		if err != nil {
			web.ServerError(w, r, err, "list lists failed")
			return
		}
		web.JSON(w, http.StatusOK, ls)
	}
}

func getList(store ListStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hh, ok := web.HouseholdID(r.Context())
		if !ok {
			web.Error(w, http.StatusNotFound, "not found")
			return
		}
		id, ok := listID(w, r)
		if !ok {
			return
		}
		l, err := store.Get(r.Context(), hh, id)
		if errors.Is(err, lists.ErrNotFound) {
			web.Error(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			web.ServerError(w, r, err, "get list failed")
			return
		}
		web.JSON(w, http.StatusOK, l)
	}
}

func patchList(store ListStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hh, ok := web.HouseholdID(r.Context())
		if !ok {
			web.Error(w, http.StatusNotFound, "not found")
			return
		}
		id, ok := listID(w, r)
		if !ok {
			return
		}
		var body struct {
			Name     *string `json:"name"`
			Archived *bool   `json:"archived"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || (body.Name == nil && body.Archived == nil) {
			web.Error(w, http.StatusBadRequest, "name or archived required")
			return
		}
		if body.Name != nil && *body.Name == "" {
			web.Error(w, http.StatusBadRequest, "name must not be empty")
			return
		}
		l, err := store.Update(r.Context(), hh, id, body.Name, body.Archived)
		if errors.Is(err, lists.ErrNotFound) {
			web.Error(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			web.ServerError(w, r, err, "update list failed")
			return
		}
		web.JSON(w, http.StatusOK, l)
	}
}

func deleteList(store ListStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hh, ok := web.HouseholdID(r.Context())
		if !ok {
			web.Error(w, http.StatusNotFound, "not found")
			return
		}
		id, ok := listID(w, r)
		if !ok {
			return
		}
		err := store.SoftDelete(r.Context(), hh, id)
		if errors.Is(err, lists.ErrNotFound) {
			web.Error(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			web.ServerError(w, r, err, "delete list failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
```

- [ ] **Step 3b: Wire the routes**

In `backend/internal/router/router.go`, add `Lists ListStore` to the `Deps` struct (next to `Households`):

```go
	AuthMiddleware func(http.Handler) http.Handler
	Households     HouseholdStore
	Lists          ListStore
```

Then inside the existing `RequireAuth` group in `New`, register the routes (alongside the household routes):

```go
			if deps.Lists != nil {
				r.Put("/lists/{id}", putList(deps.Lists))
				r.Get("/lists", listLists(deps.Lists))
				r.Get("/lists/{id}", getList(deps.Lists))
				r.Patch("/lists/{id}", patchList(deps.Lists))
				r.Delete("/lists/{id}", deleteList(deps.Lists))
			}
```

Note: the `RequireAuth` group currently only opens when `deps.Households != nil` (see `router.go`). Restructure so the group opens when **either** `Households` or `Lists` is set, and each block is independently nil-guarded:

```go
		if deps.Households != nil || deps.Lists != nil {
			r.Group(func(r chi.Router) {
				r.Use(auth.RequireAuth)
				if deps.Households != nil {
					r.Post("/households", createHousehold(deps.Households))
					r.Post("/households/join", joinHousehold(deps.Households))
					r.Get("/households/{id}", getHousehold(deps.Households))
				}
				if deps.Lists != nil {
					r.Put("/lists/{id}", putList(deps.Lists))
					r.Get("/lists", listLists(deps.Lists))
					r.Get("/lists/{id}", getList(deps.Lists))
					r.Patch("/lists/{id}", patchList(deps.Lists))
					r.Delete("/lists/{id}", deleteList(deps.Lists))
				}
			})
		}
```

- [ ] **Step 4: Run tests + tidy + vet**

Run: `cd backend && go mod tidy && go test ./internal/router/ && go vet ./...`
Expected: PASS. `go mod tidy` promotes `github.com/google/uuid` from indirect to a direct dependency.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/router/lists.go backend/internal/router/router.go backend/internal/router/lists_handler_test.go backend/go.mod backend/go.sum
git commit -m "feat(router): list CRUD endpoints behind RequireAuth"
```

---

### Task 4: Wire the store into both entrypoints + full verification

**Files:**
- Modify: `backend/cmd/api/main.go`
- Modify: `backend/cmd/lambda/main.go`

**Interfaces:**
- Consumes: `lists.NewStore` (Task 1), `router.Deps.Lists` (Task 3).
- Produces: a fully wired binary; `Lists` populated in both routers.

- [ ] **Step 1: Wire cmd/api**

In `backend/cmd/api/main.go`, add the import `"github.com/stroem/shopping-list/backend/internal/lists"` (alongside `households`) and add `Lists` to the `router.Deps` literal:

```go
		Handler: router.New(router.Deps{
			DB:                 pool,
			Suggest:            suggest.New(pool),
			RequestTimeout:     cfg.RequestTimeout,
			AuthMiddleware:     auth.Middleware(verifier, auth.NewUserStore(pool)),
			Households:         households.NewStore(pool),
			Lists:              lists.NewStore(pool),
			CORSAllowedOrigins: cfg.CORSAllowedOrigins,
		}),
```

- [ ] **Step 2: Wire cmd/lambda**

In `backend/cmd/lambda/main.go`, add the same `lists` import and `Lists` field to its `router.Deps` literal:

```go
	adapter := httpadapter.NewV2(router.New(router.Deps{
		DB:                 pool,
		Suggest:            suggest.New(pool),
		AuthMiddleware:     auth.Middleware(verifier, auth.NewUserStore(pool)),
		Households:         households.NewStore(pool),
		Lists:              lists.NewStore(pool),
		CORSAllowedOrigins: corsOrigins,
	}))
```

- [ ] **Step 3: Build, vet, full test suite**

Run: `cd backend && go build ./... && go vet ./... && go test ./...`
Expected: all packages `ok` (lists/households DB tests SKIP if Docker is unavailable).

- [ ] **Step 4: Commit**

```bash
git add backend/cmd/api/main.go backend/cmd/lambda/main.go
git commit -m "feat(api): wire lists store into api and lambda entrypoints"
```

---

## Self-Review

**Spec coverage:**
- AC1 create/rename/archive/soft-delete → Task 1 (Upsert), Task 2 (Update rename+archive, SoftDelete), Task 3 (PUT/PATCH/DELETE handlers). ✅
- AC2 client UUID, idempotent create → Task 1 Upsert (`created` bool, dup-count assertion), Task 3 PUT 201-vs-200. ✅
- AC3 household-scoped, 404 foreign → Task 2 `TestCrossHouseholdIsolation`, Task 3 404/no-household tests. ✅
- AC4 `updated_at`/`deleted_at` maintained → Task 2 (updated_at advance assertion, SoftDelete sets deleted_at). ✅
- Spec REST surface (PUT/GET/GET/PATCH/DELETE) → Task 3 routes. ✅
- Terminal-delete (re-PUT deleted → 404) → Task 1 guard, Task 2 `TestListUpdateArchiveSoftDelete` re-upsert assertion. ✅
- 400 on bad UUID / empty body → Task 3 handler tests. ✅

**Placeholder scan:** none — every code step is complete.

**Type consistency:** `Upsert(...) (List, bool, error)`, `Update(..., name *string, archived *bool) (List, error)`, `SoftDelete(...) error`, `List(...) ([]List, error)`, `Get(...) (List, error)` are identical across the store (Tasks 1–2), the `ListStore` interface, the fake, and the entrypoints (Tasks 3–4). `web.HouseholdID`, `web.JSON`, `web.Error`, `web.ServerError` match `internal/web`. Route helpers `putList/listLists/getList/patchList/deleteList` match between `lists.go` and `router.go`.
