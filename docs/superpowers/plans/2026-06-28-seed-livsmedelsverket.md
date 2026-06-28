# Seed Livsmedelsverket Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Import the ~2,575 Livsmedelsverket generic foods into `food_catalog` idempotently, with a keyword-derived `aisle`, via an `internal/catalog` package and a real `cmd/seed`.

**Architecture:** `internal/catalog` holds the aisle heuristic, the Livsmedelsverket parser, and the idempotent upsert; `cmd/seed livsmedelsverket [--file]` loads config, parses the file, opens a pool, and upserts. `data/` stays gitignored — read by path.

**Tech Stack:** Go 1.26 · jackc/pgx/v5 + pgxpool · encoding/json · testcontainers-go.

## Global Constraints

- Go module `github.com/stroem/shopping-list/backend`; `go 1.26`.
- Idempotent on `food_catalog (source, external_id)` unique key (from #2).
- `aisle` derived by Swedish-keyword heuristic; unmatched → NULL. Source = `"livsmedelsverket"`, `external_id` = the item's `nummer`.
- `cmd/seed` reads `DATABASE_URL`; default file `../data/food/livsmedelsverket_products.json`.
- DB-backed tests skip without docker; unit tests (aisle, parse) need no DB.
- Conventional Commits; **no AI attribution**; commit on `feat/issue-5-seed-livsmedelsverket` (never `main`); never commit `data/`.

## File structure

- `backend/internal/catalog/aisle.go` (+ `aisle_test.go`) — taxonomy + `AisleFor`.
- `backend/internal/catalog/catalog.go` — `Row`, `Querier`, `UpsertFood`.
- `backend/internal/catalog/livsmedelsverket.go` (+ `livsmedelsverket_test.go`) — parser.
- `backend/internal/catalog/upsert_test.go` — testcontainers idempotency.
- `backend/cmd/seed/main.go` (modify) — subcommand wiring.

---

### Task 1: Aisle taxonomy + `AisleFor` heuristic (TDD)

**Files:**
- Create: `backend/internal/catalog/aisle.go`, `backend/internal/catalog/aisle_test.go`

**Interfaces:**
- Produces: `func AisleFor(name string) *int` — first matching aisle by priority, else nil.

- [ ] **Step 1: Write the failing test**

`backend/internal/catalog/aisle_test.go`:

```go
package catalog

import "testing"

func TestAisleFor(t *testing.T) {
	cases := []struct {
		name string
		want *int
	}{
		{"Lax, rökt", intp(4)},   // fish beats everything
		{"Mjölk 3%", intp(2)},    // dairy
		{"Äpple", intp(1)},       // produce
		{"Knäckebröd", intp(5)},  // bread
		{"Pasta, fullkorn", intp(6)}, // pantry
		{"Glass, vanilj", intp(7)},   // frozen
		{"Nötfärs", intp(3)},     // meat (nöt + färs)
		{"Kaffe, bryggt", intp(8)},   // drink
		{"Choklad, mörk", intp(9)},   // candy
		{"Xyzzy okänt", nil},     // no match
	}
	for _, c := range cases {
		got := AisleFor(c.name)
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Errorf("AisleFor(%q) = %v, want %v", c.name, deref(got), deref(c.want))
		}
	}
}

func intp(i int) *int { return &i }
func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
```

- [ ] **Step 2: Run — expect FAIL** (`undefined: AisleFor`). `cd backend && go test ./internal/catalog/`

- [ ] **Step 3: Implement `backend/internal/catalog/aisle.go`**

```go
// Package catalog imports food reference data (Livsmedelsverket generics, and
// later Open Food Facts) into Postgres for autocomplete.
package catalog

import "strings"

// aisleGroup is one store section and the lowercase name-substrings that map to it.
type aisleGroup struct {
	aisle    int
	keywords []string
}

// aisleGroups is the v1 aisle taxonomy, in match-priority order: specific
// proteins (fish, meat) before generic matches. This integer space is the shared
// aisle taxonomy other features reference. Unmatched names get no aisle.
var aisleGroups = []aisleGroup{
	{4, []string{"lax", "torsk", "sill", "makrill", "tonfisk", "sej", "abborre", "räk", "krabb", "mussla", "hummer", "fisk", "skaldjur"}},
	{3, []string{"nöt", "fläsk", "kyckling", "kalkon", "korv", "bacon", "skinka", "färs", "lamm", "biff", "revben", "lever", "kött"}},
	{2, []string{"mjölk", "ost", "yoghurt", "fil", "grädde", "smör", "ägg", "kvarg", "keso", "mese", "gräddfil"}},
	{1, []string{"äpple", "banan", "apelsin", "päron", "tomat", "gurka", "sallad", "lök", "potatis", "morot", "paprika", "broccoli", "spenat", "kål", "svamp", "champinjon", "bär", "jordgubb", "frukt", "grönsak"}},
	{5, []string{"knäckebröd", "bröd", "bulle", "baguette", "fralla", "skorpa", "tortilla", "pita"}},
	{6, []string{"pasta", "ris", "mjöl", "socker", "salt", "gryn", "flingor", "müsli", "bön", "lins", "ärt", "konserv", "olja", "vinäger", "buljong", "ketchup", "senap", "sås", "krydd", "honung", "sylt"}},
	{7, []string{"fryst", "glass"}},
	{8, []string{"juice", "läsk", "saft", "kaffe", "vatten", "smoothie"}},
	{9, []string{"godis", "choklad", "chips", "kex", "snacks"}},
}

// AisleFor returns the first aisle whose keyword is a substring of the lowercased
// name, by taxonomy priority, or nil when nothing matches.
func AisleFor(name string) *int {
	n := strings.ToLower(name)
	for _, g := range aisleGroups {
		for _, kw := range g.keywords {
			if strings.Contains(n, kw) {
				a := g.aisle
				return &a
			}
		}
	}
	return nil
}
```

- [ ] **Step 4: Run — expect PASS.** `cd backend && go test ./internal/catalog/ && go vet ./internal/catalog/`

- [ ] **Step 5: Commit**

```bash
git add backend/internal/catalog/aisle.go backend/internal/catalog/aisle_test.go
git commit -m "feat(catalog): swedish keyword aisle heuristic"
```

---

### Task 2: Row type + Livsmedelsverket parser (TDD)

**Files:**
- Create: `backend/internal/catalog/catalog.go`, `backend/internal/catalog/livsmedelsverket.go`, `backend/internal/catalog/livsmedelsverket_test.go`

**Interfaces:**
- Produces:
  - `type Row struct { Source, ExternalID, Name string; Aisle *int }`
  - `func ParseLivsmedelsverket(r io.Reader) ([]Row, error)`

- [ ] **Step 1: Write the failing test**

`backend/internal/catalog/livsmedelsverket_test.go`:

```go
package catalog

import (
	"strings"
	"testing"
)

const sampleLivsmedel = `{
  "_meta": {"totalRecords": 3},
  "livsmedel": [
    {"nummer": 1, "namn": "Mjölk 3%"},
    {"nummer": 2, "namn": "Lax, rökt"},
    {"nummer": 3, "namn": ""}
  ]
}`

func TestParseLivsmedelsverket(t *testing.T) {
	rows, err := ParseLivsmedelsverket(strings.NewReader(sampleLivsmedel))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rows) != 2 { // the empty-name row is skipped
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0].Source != "livsmedelsverket" || rows[0].ExternalID != "1" || rows[0].Name != "Mjölk 3%" {
		t.Fatalf("row0 = %+v", rows[0])
	}
	if rows[0].Aisle == nil || *rows[0].Aisle != 2 {
		t.Fatalf("row0 aisle = %v, want 2 (dairy)", rows[0].Aisle)
	}
	if rows[1].Aisle == nil || *rows[1].Aisle != 4 {
		t.Fatalf("row1 aisle = %v, want 4 (fish)", rows[1].Aisle)
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (`undefined: ParseLivsmedelsverket`). `cd backend && go test ./internal/catalog/ -run TestParse`

- [ ] **Step 3: Implement `backend/internal/catalog/catalog.go` (the Row type)**

```go
package catalog

// Row is one food_catalog record to upsert.
type Row struct {
	Source     string
	ExternalID string
	Name       string
	Aisle      *int
}
```

- [ ] **Step 4: Implement `backend/internal/catalog/livsmedelsverket.go`**

```go
package catalog

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type livsmedelEnvelope struct {
	Livsmedel []struct {
		Nummer int    `json:"nummer"`
		Namn   string `json:"namn"`
	} `json:"livsmedel"`
}

// ParseLivsmedelsverket decodes the Livsmedelsverket products JSON into Rows.
// Items with an empty name are skipped; aisle is derived from the name.
func ParseLivsmedelsverket(r io.Reader) ([]Row, error) {
	var env livsmedelEnvelope
	if err := json.NewDecoder(r).Decode(&env); err != nil {
		return nil, fmt.Errorf("decode livsmedelsverket json: %w", err)
	}
	rows := make([]Row, 0, len(env.Livsmedel))
	for _, item := range env.Livsmedel {
		name := strings.TrimSpace(item.Namn)
		if name == "" {
			continue
		}
		rows = append(rows, Row{
			Source:     "livsmedelsverket",
			ExternalID: strconv.Itoa(item.Nummer),
			Name:       name,
			Aisle:      AisleFor(name),
		})
	}
	return rows, nil
}
```

- [ ] **Step 5: Run — expect PASS.** `cd backend && go test ./internal/catalog/`

- [ ] **Step 6: Commit**

```bash
git add backend/internal/catalog/catalog.go backend/internal/catalog/livsmedelsverket.go backend/internal/catalog/livsmedelsverket_test.go
git commit -m "feat(catalog): parse livsmedelsverket products json"
```

---

### Task 3: Idempotent `UpsertFood` (testcontainers)

**Files:**
- Modify: `backend/internal/catalog/catalog.go`
- Create: `backend/internal/catalog/upsert_test.go`

**Interfaces:**
- Consumes: `Row`, the `food_catalog` schema, `db.Migrate`.
- Produces:
  - `type Querier interface { QueryRow(ctx context.Context, sql string, args ...any) pgx.Row }`
  - `func UpsertFood(ctx context.Context, db Querier, rows []Row) (inserted, updated int, err error)`

- [ ] **Step 1: Add `UpsertFood` to `backend/internal/catalog/catalog.go`**

```go
package catalog

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Querier is the slice of pgx used by UpsertFood. *pgxpool.Pool satisfies it.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const upsertFoodSQL = `
INSERT INTO food_catalog (source, external_id, name, aisle)
VALUES ($1, $2, $3, $4)
ON CONFLICT (source, external_id)
DO UPDATE SET name = EXCLUDED.name, aisle = EXCLUDED.aisle, updated_at = now()
RETURNING (xmax = 0) AS inserted`

// UpsertFood inserts or updates each row, idempotently, keyed by
// (source, external_id). It returns how many rows were newly inserted vs updated.
// The `xmax = 0` trick distinguishes a fresh insert from an update.
func UpsertFood(ctx context.Context, db Querier, rows []Row) (inserted, updated int, err error) {
	for _, r := range rows {
		var wasInsert bool
		if err = db.QueryRow(ctx, upsertFoodSQL, r.Source, r.ExternalID, r.Name, r.Aisle).Scan(&wasInsert); err != nil {
			return inserted, updated, fmt.Errorf("upsert %s/%s: %w", r.Source, r.ExternalID, err)
		}
		if wasInsert {
			inserted++
		} else {
			updated++
		}
	}
	return inserted, updated, nil
}
```

- [ ] **Step 2: Write `backend/internal/catalog/upsert_test.go`**

```go
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
```

- [ ] **Step 3: Run the catalog suite + vet**

Run: `cd backend && go test ./internal/catalog/ && go vet ./internal/catalog/`
Expected: aisle + parse pass; upsert passes (docker present) or skips.

- [ ] **Step 4: Commit**

```bash
git add backend/internal/catalog/catalog.go backend/internal/catalog/upsert_test.go
git commit -m "feat(catalog): idempotent food_catalog upsert"
```

---

### Task 4: Wire `cmd/seed`

**Files:**
- Modify: `backend/cmd/seed/main.go`

**Interfaces:**
- Consumes: `config.Load`, `db.NewPool`, `catalog.ParseLivsmedelsverket`, `catalog.UpsertFood`.

- [ ] **Step 1: Rewrite `backend/cmd/seed/main.go`**

```go
// Command seed imports food reference data into Postgres. It reads DATABASE_URL.
//
//	go run ./cmd/seed livsmedelsverket [--file <path>]
//
// Open Food Facts (--> ean_mappings) is added by a later subcommand.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/stroem/shopping-list/backend/internal/catalog"
	"github.com/stroem/shopping-list/backend/internal/config"
	"github.com/stroem/shopping-list/backend/internal/db"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "livsmedelsverket":
		if err := seedLivsmedelsverket(os.Args[2:]); err != nil {
			log.Fatalf("seed livsmedelsverket: %v", err)
		}
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: seed livsmedelsverket [--file <path>]")
	os.Exit(2)
}

func seedLivsmedelsverket(args []string) error {
	fs := flag.NewFlagSet("livsmedelsverket", flag.ExitOnError)
	file := fs.String("file", "../data/food/livsmedelsverket_products.json", "path to livsmedelsverket products json")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	f, err := os.Open(*file)
	if err != nil {
		return fmt.Errorf("open %s: %w", *file, err)
	}
	defer f.Close()

	rows, err := catalog.ParseLivsmedelsverket(f)
	if err != nil {
		return err
	}

	ctx := context.Background()
	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	inserted, updated, err := catalog.UpsertFood(ctx, pool, rows)
	if err != nil {
		return err
	}
	log.Printf("livsmedelsverket: %d parsed, %d inserted, %d updated", len(rows), inserted, updated)
	return nil
}
```

- [ ] **Step 2: Build, vet, full test**

Run: `cd backend && go build ./... && go vet ./... && go test ./...`
Expected: all green (catalog tests pass/skip; everything else unaffected).

- [ ] **Step 3: Commit**

```bash
git add backend/cmd/seed/main.go
git commit -m "feat(seed): import livsmedelsverket into food_catalog"
```

---

## Self-Review

**Spec coverage:**
- Idempotent upsert (AC1) → Task 3 `UpsertFood` (ON CONFLICT) + idempotency test. ✓
- `huvudgrupp`→aisle replaced by keyword heuristic (data reality) → Task 1 `AisleFor`. ✓
- Names indexed (AC3) → already in #2 (no task needed; noted). ✓
- Parser + Row + cmd/seed subcommand → Tasks 2 + 4. ✓
- `data/` read by path, never committed → Task 4 default `--file`. ✓

**Placeholder scan:** No TBD/TODO; every code block complete. The `xmax = 0` returning clause is real Postgres. ✓

**Type consistency:** `Row{Source,ExternalID,Name,Aisle}` consistent across parser, upsert, tests. `AisleFor(name) *int` matches all call sites. `Querier.QueryRow(ctx, sql, args...) pgx.Row` matches `*pgxpool.Pool`'s method and the test passes a real pool. `UpsertFood(ctx, db, rows) (inserted, updated int, err error)` consistent in catalog.go, the test, and cmd/seed. ✓
