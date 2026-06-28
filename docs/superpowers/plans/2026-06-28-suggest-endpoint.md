# Autocomplete /v1/suggest Endpoint Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `GET /v1/suggest?q=` — a household-scoped, frequency-ranked autocomplete endpoint over `items` → `food_catalog` → `ean_mappings`, every result carrying an `aisle`.

**Architecture:** A new `internal/suggest` package owns the household-resolution seam and a single ranked SQL query (returns a `[]Suggestion`). A thin `internal/router` handler mounted under `/v1` reads `q`/`limit`/`X-Device-Id` and delegates. `cmd/api` and `cmd/lambda` construct the service from the pgx pool and pass it via `router.Deps`.

**Tech Stack:** Go 1.26 · go-chi/chi v5 · jackc/pgx/v5 + pgxpool · pg_trgm (existing GIN indexes) · testcontainers-go.

## Global Constraints

- Go module `github.com/stroem/shopping-list/backend`; `go 1.26`.
- Endpoint `GET /v1/suggest?q=<text>&limit=<n>`; `limit` default **10**, clamped to `[1,25]`; empty/whitespace `q` → `200 []` (no DB hit).
- Response: JSON array of `{name, aisle, source, image_url, ean}`; `aisle` nullable int; `source` ∈ `items|food_catalog|openfoodfacts`; `ean` set **only** for `openfoodfacts` rows; never `null` (return `[]`).
- Sources ranked items(0) → food_catalog(1) → ean_mappings(2); match `name ILIKE q||'%' OR name % q`; dedup by `lower(name)`.
- Household resolved behind a swappable seam: `X-Device-Id` → `users.household_id`; unknown/missing → catalog-only. No cross-household leakage.
- DB-backed tests skip cleanly without docker. Conventional Commits; **no AI attribution**; commit on `feat/issue-7-suggest-endpoint` only.

## File structure

- `backend/internal/suggest/suggest.go` (+ `suggest_test.go`) — `Suggestion`, `Querier`, `Service`, `New`, `clampLimit`, `Suggest`, `resolveHousehold`.
- `backend/internal/suggest/suggest_db_test.go` — testcontainers ranking/isolation test.
- `backend/internal/router/suggest.go` (+ cases in `suggest_handler_test.go`) — `Suggester` interface, `suggestHandler`.
- `backend/internal/router/router.go` (modify) — `Deps.Suggest`, `/v1` route group.
- `backend/cmd/api/main.go`, `backend/cmd/lambda/main.go` (modify) — wire `suggest.New(pool)`.

---

### Task 1: `internal/suggest` service core (TDD)

**Files:**
- Create: `backend/internal/suggest/suggest.go`, `backend/internal/suggest/suggest_test.go`

**Interfaces:**
- Produces:
  - `type Suggestion struct { Name string; Aisle *int; Source string; ImageURL *string; EAN *string }`
  - `type Querier interface { Query(ctx, sql string, args ...any) (pgx.Rows, error); QueryRow(ctx, sql string, args ...any) pgx.Row }`
  - `func New(db Querier) *Service`
  - `func (s *Service) Suggest(ctx context.Context, deviceID, q string, limit int) ([]Suggestion, error)`

- [ ] **Step 1: Write the failing test**

`backend/internal/suggest/suggest_test.go`:

```go
package suggest

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

// failQuerier fails if any query runs — proves the empty-q path never hits the DB.
type failQuerier struct{}

func (failQuerier) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("Query must not be called")
}
func (failQuerier) QueryRow(context.Context, string, ...any) pgx.Row {
	return errRow{}
}

type errRow struct{}

func (errRow) Scan(...any) error { return errors.New("QueryRow must not be called") }

func TestSuggest_EmptyQueryReturnsEmptyWithoutDB(t *testing.T) {
	s := New(failQuerier{})
	for _, q := range []string{"", "   ", "\t"} {
		got, err := s.Suggest(context.Background(), "dev-1", q, 10)
		if err != nil {
			t.Fatalf("q=%q: unexpected err %v", q, err)
		}
		if len(got) != 0 {
			t.Fatalf("q=%q: got %d results, want 0", q, len(got))
		}
	}
}

func TestClampLimit(t *testing.T) {
	cases := map[int]int{0: 10, -5: 10, 1: 1, 10: 10, 25: 25, 26: 25, 999: 25}
	for in, want := range cases {
		if got := clampLimit(in); got != want {
			t.Errorf("clampLimit(%d) = %d, want %d", in, got, want)
		}
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (`undefined: New` / `clampLimit`). `cd backend && go test ./internal/suggest/`

- [ ] **Step 3: Implement `backend/internal/suggest/suggest.go`**

```go
// Package suggest powers the add-item autocomplete endpoint: it ranks a
// household's own items by purchase frequency, then the generic food_catalog,
// then branded ean_mappings, all carrying an aisle for list sorting.
package suggest

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Suggestion is one ranked autocomplete result.
type Suggestion struct {
	Name     string  `json:"name"`
	Aisle    *int    `json:"aisle"`
	Source   string  `json:"source"`
	ImageURL *string `json:"image_url"`
	EAN      *string `json:"ean"`
}

// Querier is the slice of pgx the service needs. *pgxpool.Pool satisfies it.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Service answers suggestion queries.
type Service struct {
	db Querier
}

// New builds a Service over the given querier.
func New(db Querier) *Service { return &Service{db: db} }

const (
	defaultLimit = 10
	maxLimit     = 25
)

func clampLimit(n int) int {
	if n < 1 {
		return defaultLimit
	}
	if n > maxLimit {
		return maxLimit
	}
	return n
}

// suggestSQL ranks items(0) → food_catalog(1) → ean_mappings(2), matching prefix
// or trigram, deduping by lower(name) and keeping the best source. $1 = query,
// $2 = household id (text-cast to uuid; NULL ⇒ catalog-only), $3 = limit.
const suggestSQL = `
WITH matches AS (
    SELECT name, aisle, 'items' AS source, 0 AS src_rank, image_url, NULL::text AS ean,
           purchase_count, (name ILIKE $1 || '%') AS is_prefix, similarity(name, $1) AS sim
    FROM items
    WHERE deleted_at IS NULL AND household_id = $2::uuid
      AND (name ILIKE $1 || '%' OR name % $1)
    UNION ALL
    SELECT name, aisle, 'food_catalog', 1, image_url, NULL,
           0, (name ILIKE $1 || '%'), similarity(name, $1)
    FROM food_catalog
    WHERE deleted_at IS NULL AND (name ILIKE $1 || '%' OR name % $1)
    UNION ALL
    SELECT name, aisle, 'openfoodfacts', 2, image_url, ean,
           0, (name ILIKE $1 || '%'), similarity(name, $1)
    FROM ean_mappings
    WHERE deleted_at IS NULL AND (name ILIKE $1 || '%' OR name % $1)
),
ranked AS (
    SELECT DISTINCT ON (lower(name)) name, aisle, source, image_url, ean,
           src_rank, purchase_count, is_prefix, sim
    FROM matches
    ORDER BY lower(name), src_rank, purchase_count DESC, is_prefix DESC, sim DESC
)
SELECT name, aisle, source, image_url, ean
FROM ranked
ORDER BY src_rank, purchase_count DESC, is_prefix DESC, sim DESC, length(name), name
LIMIT $3`

// Suggest returns ranked suggestions for q, scoped to the household resolved from
// deviceID. Empty/whitespace q returns no results without touching the DB.
func (s *Service) Suggest(ctx context.Context, deviceID, q string, limit int) ([]Suggestion, error) {
	q = strings.TrimSpace(q)
	out := []Suggestion{}
	if q == "" {
		return out, nil
	}

	household, err := s.resolveHousehold(ctx, deviceID)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.Query(ctx, suggestSQL, q, household, clampLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var sug Suggestion
		if err := rows.Scan(&sug.Name, &sug.Aisle, &sug.Source, &sug.ImageURL, &sug.EAN); err != nil {
			return nil, err
		}
		out = append(out, sug)
	}
	return out, rows.Err()
}

// resolveHousehold is the identity seam: provisionally maps X-Device-Id →
// users.household_id. #8 (Google auth + invite links) swaps this body. An unknown
// device or a user with no household returns nil ⇒ catalog-only suggestions.
func (s *Service) resolveHousehold(ctx context.Context, deviceID string) (*string, error) {
	if deviceID == "" {
		return nil, nil
	}
	var hh *string
	err := s.db.QueryRow(ctx,
		`SELECT household_id::text FROM users WHERE device_id = $1 AND deleted_at IS NULL`,
		deviceID,
	).Scan(&hh)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return hh, nil
}
```

- [ ] **Step 4: Run — expect PASS.** `cd backend && go test ./internal/suggest/ && go vet ./internal/suggest/`

- [ ] **Step 5: Commit**

```bash
git add backend/internal/suggest/suggest.go backend/internal/suggest/suggest_test.go
git commit -m "feat(suggest): ranked autocomplete service over items, catalog, ean"
```

---

### Task 2: Ranking & isolation, against real Postgres (testcontainers)

**Files:**
- Create: `backend/internal/suggest/suggest_db_test.go`

**Interfaces:**
- Consumes: `New`, `Service.Suggest`, the `items`/`food_catalog`/`ean_mappings`/`users` schema, `db.Migrate`.

- [ ] **Step 1: Write the integration test**

`backend/internal/suggest/suggest_db_test.go`:

```go
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
```

- [ ] **Step 2: Run — expect PASS (or SKIP without docker)**

Run: `cd backend && go test ./internal/suggest/ -run TestSuggestRanking -v`
Expected: PASS with docker (items-first, dedup, isolation, ean-only-branded, limit), or SKIP.

- [ ] **Step 3: Commit**

```bash
git add backend/internal/suggest/suggest_db_test.go
git commit -m "test(suggest): ranking, dedup and household isolation against postgres"
```

---

### Task 3: HTTP handler + `/v1` wiring

**Files:**
- Create: `backend/internal/router/suggest.go`, `backend/internal/router/suggest_handler_test.go`
- Modify: `backend/internal/router/router.go`, `backend/cmd/api/main.go`, `backend/cmd/lambda/main.go`

**Interfaces:**
- Consumes: `suggest.Suggestion`, `suggest.New`, `web.JSON`/`web.Error`/`web.DeviceID`.
- Produces:
  - `type Suggester interface { Suggest(ctx context.Context, deviceID, q string, limit int) ([]suggest.Suggestion, error) }`
  - `Deps.Suggest Suggester`; route `GET /v1/suggest`.

- [ ] **Step 1: Write the failing handler test**

`backend/internal/router/suggest_handler_test.go`:

```go
package router_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stroem/shopping-list/backend/internal/router"
	"github.com/stroem/shopping-list/backend/internal/suggest"
)

type fakeSuggester struct {
	gotDevice, gotQ string
	gotLimit        int
	out             []suggest.Suggestion
	err             error
}

func (f *fakeSuggester) Suggest(_ context.Context, deviceID, q string, limit int) ([]suggest.Suggestion, error) {
	f.gotDevice, f.gotQ, f.gotLimit = deviceID, q, limit
	return f.out, f.err
}

func doSuggest(t *testing.T, fake *fakeSuggester, target, deviceID string) *httptest.ResponseRecorder {
	t.Helper()
	h := router.New(router.Deps{DB: fakePinger{}, Suggest: fake})
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if deviceID != "" {
		req.Header.Set("X-Device-Id", deviceID)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSuggestHandler_PassesParamsAndReturnsArray(t *testing.T) {
	aisle := 2
	fake := &fakeSuggester{out: []suggest.Suggestion{{Name: "Mjölk 3%", Aisle: &aisle, Source: "food_catalog"}}}
	rec := doSuggest(t, fake, "/v1/suggest?q=mj&limit=5", "dev-9")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if fake.gotDevice != "dev-9" || fake.gotQ != "mj" || fake.gotLimit != 5 {
		t.Fatalf("passthrough = %q/%q/%d, want dev-9/mj/5", fake.gotDevice, fake.gotQ, fake.gotLimit)
	}
	var got []suggest.Suggestion
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body not a JSON array: %v (%s)", err, rec.Body.String())
	}
	if len(got) != 1 || got[0].Name != "Mjölk 3%" {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestSuggestHandler_EmptyResultIsArrayNotNull(t *testing.T) {
	rec := doSuggest(t, &fakeSuggester{out: nil}, "/v1/suggest?q=zzz", "")
	if body := rec.Body.String(); body != "[]\n" {
		t.Fatalf("empty body = %q, want %q", body, "[]\n")
	}
}

func TestSuggestHandler_BadLimitDefaultsToZero(t *testing.T) {
	fake := &fakeSuggester{}
	doSuggest(t, fake, "/v1/suggest?q=mj&limit=abc", "")
	if fake.gotLimit != 0 { // service clamps 0 → default
		t.Fatalf("limit = %d, want 0 (service clamps)", fake.gotLimit)
	}
}

func TestSuggestHandler_ServiceErrorIs500(t *testing.T) {
	rec := doSuggest(t, &fakeSuggester{err: errors.New("boom")}, "/v1/suggest?q=mj", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}
```

(`fakePinger` already exists in `router_test.go`.)

- [ ] **Step 2: Run — expect FAIL** (`unknown field Suggest` / no `/v1/suggest`). `cd backend && go test ./internal/router/`

- [ ] **Step 3: Add the handler `backend/internal/router/suggest.go`**

```go
package router

import (
	"context"
	"net/http"
	"strconv"

	"github.com/stroem/shopping-list/backend/internal/suggest"
	"github.com/stroem/shopping-list/backend/internal/web"
)

// Suggester is the autocomplete dependency the suggest handler needs.
// *suggest.Service satisfies it; tests pass a fake.
type Suggester interface {
	Suggest(ctx context.Context, deviceID, q string, limit int) ([]suggest.Suggestion, error)
}

// suggestHandler answers GET /v1/suggest?q=&limit=, scoped to the caller's
// household (resolved by the service from X-Device-Id).
func suggestHandler(s Suggester) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit")) // 0 on absent/bad → service clamps
		deviceID := web.DeviceID(r.Context())

		results, err := s.Suggest(r.Context(), deviceID, q, limit)
		if err != nil {
			web.Error(w, http.StatusInternalServerError, "suggest failed")
			return
		}
		web.JSON(w, http.StatusOK, results)
	}
}
```

- [ ] **Step 4: Wire into `backend/internal/router/router.go`**

Add the `Suggest` field to `Deps` and the `/v1` route group:

```go
// Deps are the runtime dependencies the router wires into handlers.
type Deps struct {
	DB      Pinger
	Suggest Suggester
}
```

and inside `New`, after `r.Get("/healthz", healthz(deps.DB))`:

```go
	r.Route("/v1", func(r chi.Router) {
		r.Get("/suggest", suggestHandler(deps.Suggest))
	})
```

- [ ] **Step 5: Run — expect PASS.** `cd backend && go test ./internal/router/ && go vet ./internal/router/`

- [ ] **Step 6: Wire the real service into both entrypoints**

`backend/cmd/api/main.go` — replace the `Handler:` line:

```go
		Handler:           router.New(router.Deps{DB: pool, Suggest: suggest.New(pool)}),
```

and add `"github.com/stroem/shopping-list/backend/internal/suggest"` to its imports.

`backend/cmd/lambda/main.go` — replace the adapter line:

```go
	adapter := httpadapter.NewV2(router.New(router.Deps{DB: pool, Suggest: suggest.New(pool)}))
```

and add the same import.

- [ ] **Step 7: Build, vet, full test**

Run: `cd backend && go build ./... && go vet ./... && go test ./...`
Expected: all green (suggest unit + router handler tests pass; DB-backed suggest/db + others pass or skip).

- [ ] **Step 8: Commit**

```bash
git add backend/internal/router/suggest.go backend/internal/router/suggest_handler_test.go backend/internal/router/router.go backend/cmd/api/main.go backend/cmd/lambda/main.go
git commit -m "feat(router): GET /v1/suggest autocomplete endpoint"
```

---

### Task 4: Real end-to-end verification (no new code)

**Files:** none (verification only).

- [ ] **Step 1: Start Postgres, migrate, seed a little data, run the server**

```bash
cid=$(podman run -d --rm -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=shopping_list -p 55434:5432 postgres:16-alpine)
sleep 4
export DATABASE_URL="postgres://postgres:postgres@localhost:55434/shopping_list?sslmode=disable"
cd backend
go run ./cmd/migrate up
go run ./cmd/seed livsmedelsverket --file /home/stroem/code/github/stroem/shopping-list/data/food/livsmedelsverket_products.json
go run ./cmd/api &   # listens on :8080
sleep 2
```

- [ ] **Step 2: Hit the endpoint**

```bash
curl -s 'http://localhost:8080/v1/suggest?q=mj%C3%B6lk&limit=5' | head -c 600; echo
curl -s 'http://localhost:8080/v1/suggest?q=' ; echo            # expect []
```

Expected: the first returns a JSON array of catalog matches for "mjölk" (source `food_catalog`/`openfoodfacts`, each with an `aisle`); the empty-q returns `[]`.

- [ ] **Step 3: Stop the server + container**

```bash
kill %1 2>/dev/null
podman stop "$cid"
```

No commit (verification only).

---

## Self-Review

**Spec coverage:**
- `GET /v1/suggest?q=&limit=`, default/cap limit, empty-q → `[]` → Task 1 (`clampLimit`, empty-q) + Task 3 (handler). ✓
- Ranked items→food_catalog→ean_mappings, prefix-or-trigram, dedup → Task 1 SQL, proven in Task 2. ✓
- Household seam (`X-Device-Id`→users, unknown→catalog-only, no leakage) → Task 1 `resolveHousehold`, proven in Task 2. ✓
- Response fields incl. `ean` only for branded, `aisle` present, array-not-null → Task 1 `Suggestion` + Task 3 (`[]`), asserted in Task 2 + Task 3. ✓
- `/v1` group, `Deps.Suggest`, both entrypoints wired → Task 3. ✓
- DB error → 500 → Task 3 handler + test. ✓
- Real end-to-end → Task 4. ✓
- Out of scope (Google auth #8, UI #15, items writes #11) → not implemented (correct). ✓

**Placeholder scan:** No TBD/TODO; every code block complete. SQL is concrete and uses real pg_trgm operators (`%`, `similarity`) from the 0001 extension.

**Type consistency:** `Suggestion{Name,Aisle,Source,ImageURL,EAN}` identical in Task 1 (def), Task 2 (asserts), Task 3 (handler + fake). `Suggest(ctx, deviceID, q string, limit int) ([]Suggestion, error)` matches across the `Service` method, the router `Suggester` interface, and both fakes. `Querier` (Query + QueryRow) is satisfied by `*pgxpool.Pool` (used in Task 2) and the `failQuerier` fake (Task 1). `clampLimit` semantics (0/neg→10, >25→25) consistent between its test and the handler's "bad limit → 0 → service clamps" test. `Deps.Suggest Suggester` set in Task 3 and both entrypoints.
