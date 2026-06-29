# Huvudgrupp food-group enrichment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Populate `food_catalog.food_group` from the Livsmedelsverket EuroFIR group facet and drive `aisle` from a food-group→aisle table, falling back to the name keyword heuristic.

**Architecture:** A new `food_group.go` holds `ParseKlassificeringar` (build `nummer→group` from the fetched klassificeringar file) and `FoodGroupAisle` (a static EuroFIR-group→aisle table). `ParseLivsmedelsverket` is extended to take the group map and set each `Row`'s `FoodGroup` + group-preferred `Aisle`. `Row`/`upsertFoodSQL`/`UpsertFood` persist `food_group`. `cmd/seed livsmedelsverket` gains a `--klass` flag.

**Tech Stack:** Go 1.26, standard library (`encoding/json`, `strings`), existing `internal/catalog`, pgx, testcontainers (DB tests).

## Global Constraints

- No schema migration: `food_catalog.food_group text` already exists (`backend/migrations/0001_init.up.sql:127`).
- No new third-party dependency. Green bar = `go test ./...` from `backend/`; DB-backed tests skip cleanly without Docker.
- Idempotent/re-runnable upsert on `(source, external_id)`.
- Food group is stored trimmed (`strings.TrimSpace`) so the table key matches (one source group has a trailing space).
- Precedence: `FoodGroupAisle(group)` when a group maps; else `AisleFor(name)`; else nil. `food_group` set whenever the EuroFIR facet is present.
- Conventional Commits 1.0.0; **no AI/Claude attribution**. Commit only on branch `feat/issue-28-huvudgrupp-enrichment`; never `main`.

---

### Task 1: `food_group.go` — klassificeringar parser + EuroFIR→aisle table

**Files:**
- Create: `backend/internal/catalog/food_group.go`
- Test: `backend/internal/catalog/food_group_test.go`

**Interfaces:**
- Consumes: nothing from other tasks.
- Produces:
  - `func ParseKlassificeringar(r io.Reader) (map[int]string, error)` — `nummer → trimmed EuroFIR group name`; only products with the `"A Gruppindelning EuroFIR"` facet appear.
  - `func FoodGroupAisle(group string) *int` — aisle for a EuroFIR group (trimmed), or nil.

- [ ] **Step 1: Write the failing tests**

Create `backend/internal/catalog/food_group_test.go`:

```go
package catalog

import (
	"strings"
	"testing"
)

const sampleKlass = `[
  {"nummer": 10, "namn": "Mjölk 3%", "klassificeringar": [
    {"typ":"LanguaL","fasett":"B Artklassificering","fasettkod":"B1","namn":"Ko"},
    {"typ":"LanguaL","fasett":"A Gruppindelning EuroFIR","fasettkod":"A1","namn":"Mjölk"}
  ]},
  {"nummer": 20, "namn": "Lax", "klassificeringar": [
    {"typ":"LanguaL","fasett":"A Gruppindelning EuroFIR","fasettkod":"A2","namn":"Fisk"}
  ]},
  {"nummer": 30, "namn": "Mystisk", "klassificeringar": [
    {"typ":"LanguaL","fasett":"B Artklassificering","fasettkod":"B9","namn":"Okänd"}
  ]}
]`

func TestParseKlassificeringar(t *testing.T) {
	m, err := ParseKlassificeringar(strings.NewReader(sampleKlass))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(m) != 2 { // nummer 30 has no EuroFIR group facet
		t.Fatalf("got %d entries, want 2: %v", len(m), m)
	}
	if m[10] != "Mjölk" || m[20] != "Fisk" {
		t.Fatalf("map = %v, want {10:Mjölk, 20:Fisk}", m)
	}
	if _, ok := m[30]; ok {
		t.Fatalf("nummer 30 should be absent (no EuroFIR facet)")
	}
}

func TestParseKlassificeringarTrimsGroup(t *testing.T) {
	const withSpace = `[{"nummer":1,"namn":"x","klassificeringar":[
		{"fasett":"A Gruppindelning EuroFIR","namn":"Konfekt och annan sockerprodukt dvs ej choklad "}]}]`
	m, err := ParseKlassificeringar(strings.NewReader(withSpace))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m[1] != "Konfekt och annan sockerprodukt dvs ej choklad" {
		t.Fatalf("group not trimmed: %q", m[1])
	}
}

func TestFoodGroupAisle(t *testing.T) {
	cases := map[string]*int{
		"Mjölk":               intp(2),
		"Rött kött":           intp(3),
		"Fisk":                intp(4),
		"Frukt och bär":       intp(1),
		"Jäst bröd":           intp(5),
		"Pasta och liknande produkter": intp(6),
		"Glass och annan frusen dessert med mejeriprodukter": intp(7),
		"Juice och nektar":    intp(8),
		"Choklad eller chokladprodukt": intp(9),
		"Konfekt och annan sockerprodukt dvs ej choklad": intp(9), // trimmed key
		"Vin och vinliknande drycker": nil,                        // alcohol: out of scope, unmapped
		"Kosttillskott och hälsopreparat": nil,                    // supplements: unmapped
		"Totally Unknown Group": nil,
	}
	for group, want := range cases {
		got := FoodGroupAisle(group)
		if (got == nil) != (want == nil) || (got != nil && *got != *want) {
			t.Errorf("FoodGroupAisle(%q) = %v, want %v", group, deref(got), deref(want))
		}
	}
}
```

(`intp` and `deref` already exist in the `catalog` package's `aisle_test.go`.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/catalog/ -run 'TestParseKlassificeringar|TestFoodGroupAisle' -v`
Expected: compile failure — `ParseKlassificeringar`, `FoodGroupAisle` undefined.

- [ ] **Step 3: Implement `food_group.go`**

Create `backend/internal/catalog/food_group.go`:

```go
package catalog

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// eurofirFacet is the LanguaL/EuroFIR facet name carrying the food group.
const eurofirFacet = "A Gruppindelning EuroFIR"

type klassRecord struct {
	Nummer          int `json:"nummer"`
	Klassificeringar []struct {
		Fasett string `json:"fasett"`
		Namn   string `json:"namn"`
	} `json:"klassificeringar"`
}

// ParseKlassificeringar reads the Livsmedelsverket klassificeringar dump and
// returns nummer -> EuroFIR food group name (trimmed). Products without the
// "A Gruppindelning EuroFIR" facet are omitted.
func ParseKlassificeringar(r io.Reader) (map[int]string, error) {
	var recs []klassRecord
	if err := json.NewDecoder(r).Decode(&recs); err != nil {
		return nil, fmt.Errorf("decode klassificeringar json: %w", err)
	}
	out := make(map[int]string, len(recs))
	for _, rec := range recs {
		for _, f := range rec.Klassificeringar {
			if f.Fasett == eurofirFacet {
				if g := strings.TrimSpace(f.Namn); g != "" {
					out[rec.Nummer] = g
				}
				break
			}
		}
	}
	return out, nil
}

// foodGroupAisles maps Livsmedelsverket EuroFIR food groups to the v1 aisle
// taxonomy. Keys are trimmed group names exactly as they appear in the data.
// Alcohol groups (out of scope per AGENTS.md), dietary supplements, and a few
// generic catch-all groups are intentionally omitted: such products get a
// food_group but fall back to the name heuristic for aisle.
var foodGroupAisles = map[string]int{
	// 1 produce
	"Grönsaker och svamp":                1,
	"Frukt och bär":                      1,
	"Grönsaksrätter":                     1,
	"Potatisrätter":                      1,
	"Grönsaksprodukter":                  1,
	"Potatis och stärkelserika rötter":   1,
	"Grönsaker, rotfrukter och svamp":    1,
	"Färdigsallad":                       1,
	"Svamprätter":                        1,
	"Vegetariska produkter":              1,
	// 2 dairy & eggs
	"Fil och yoghurt":          2,
	"Vegetabiliska mejeriprodukter": 2,
	"Färskost":                 2,
	"Margarin och blandade fetter": 2,
	"Grädde":                   2,
	"Övriga ostprodukter":      2,
	"Ägg":                      2,
	"Äggrätter":                2,
	"Mjukost":                  2,
	"Mjölk":                    2,
	"Hårdost":                  2,
	"Halvhård ost":             2,
	"Mejeriprodukter":          2,
	"Övriga mjölkprodukter":    2,
	"Smör":                     2,
	"Extra hårdost":            2,
	"Ost":                      2,
	// 3 meat
	"Rött kött":                3,
	"Kötträtt":                 3,
	"Korv eller liknande produkt": 3,
	"Fågel":                    3,
	"Innanmat och inälvsmat":   3,
	"Kött eller köttprodukter": 3,
	// 4 fish & seafood
	"Fisk- och skaldjursrätt":  4,
	"Fisk":                     4,
	"Fisk- och skaldjursprodukt": 4,
	"Fisk och skaldjur":        4,
	// 5 bread & bakery
	"Bageriprodukter, söta och/eller feta": 5,
	"Ojäst bröd":               5,
	"Jäst bröd":                5,
	"Pannkaka eller våffla":    5,
	"Övriga bröd":              5,
	"Smörgåsar":                5,
	// 6 pantry
	"Sås i maträtt":            6,
	"Cerealierätter t.ex. klimp, risotto, pannkakor med fyllning, couscous, smörgåsar": 6,
	"Ris eller annat spannmål": 6,
	"Soppa":                    6,
	"Baljväxter":               6,
	"Processad frukt och bär":  6,
	"Matpaj eller pizza":       6,
	"Frukostflingor":           6,
	"Kryddning eller extrakt":  6,
	"Baljväxträtter":           6,
	"Cerealier eller cerealielika mjölprodukter och derivat": 6,
	"Sylt eller marmelad":      6,
	"Pasta och liknande produkter": 6,
	"Pastarätter":              6,
	"Smaksättare, tex ketchup, sojasås, senap": 6,
	"Vegetabiliskt fett och olja": 6,
	"Dressing, majonnäs":       6,
	"Konserverat kött":         6,
	"Krydda":                   6,
	"Socker, honung eller sirap": 6,
	"Spannmål och spannmålsprodukter": 6,
	"Kryddor smaksättare dressing röror": 6,
	"Andra djurfetter":         6,
	"Baknings ingrediens":      6,
	"Socker och söta livsmedel": 6,
	"Chutney eller pickle":     6,
	"Smakämne eller essencer":  6,
	// 7 frozen
	"Glass och annan frusen dessert med mejeriprodukter": 7,
	// 8 beverages (non-alcoholic)
	"Dryck utan alkohol":       8,
	"Juice och nektar":         8,
	"Kaffe, te och kakao":      8,
	"Läsk":                     8,
	"Vatten":                   8,
	// 9 snacks & sweets
	"Choklad eller chokladprodukt": 9,
	"Dessert":                  9,
	"Konfekt och annan sockerprodukt dvs ej choklad": 9,
	"Snacks":                   9,
	"Nöt eller frö produkt":    9,
	"Nöt, frö eller kärna":     9,
	"Dessertsås":               9,
	"Söta kakor":               9,
}

// FoodGroupAisle returns the v1 aisle for a EuroFIR food group, or nil when the
// group is unknown or intentionally unmapped (alcohol, supplements, generic
// catch-alls). The group is trimmed before lookup.
func FoodGroupAisle(group string) *int {
	if a, ok := foodGroupAisles[strings.TrimSpace(group)]; ok {
		return &a
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/catalog/ -run 'TestParseKlassificeringar|TestFoodGroupAisle' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/catalog/food_group.go internal/catalog/food_group_test.go
git commit -m "feat(catalog): EuroFIR food-group parser and aisle table

Add ParseKlassificeringar (nummer -> trimmed EuroFIR group) and
FoodGroupAisle (static EuroFIR group -> v1 aisle table covering the 93
groups in the Livsmedelsverket data; alcohol/supplements/generic groups
intentionally unmapped).

Part of #28"
```

---

### Task 2: Persist `food_group` and enrich `ParseLivsmedelsverket`

**Files:**
- Modify: `backend/internal/catalog/catalog.go` (`Row.FoodGroup`; `upsertFoodSQL`; `UpsertFood`)
- Modify: `backend/internal/catalog/livsmedelsverket.go` (`ParseLivsmedelsverket` signature + enrichment)
- Modify: `backend/internal/catalog/livsmedelsverket_test.go` (update call; add enrichment cases)
- Modify: `backend/internal/catalog/upsert_test.go` (food_group round-trip)

**Interfaces:**
- Consumes: `ParseKlassificeringar`, `FoodGroupAisle` (Task 1); `AisleFor` (existing).
- Produces:
  - `Row` field `FoodGroup *string`.
  - `func ParseLivsmedelsverket(r io.Reader, groups map[int]string) ([]Row, error)`.

- [ ] **Step 1: Update the existing parser test to the new signature + add enrichment cases**

In `backend/internal/catalog/livsmedelsverket_test.go`, change the existing call and add a new test. Replace the file body with:

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
	rows, err := ParseLivsmedelsverket(strings.NewReader(sampleLivsmedel), nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rows) != 2 { // the empty-name row is skipped
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0].Source != "livsmedelsverket" || rows[0].ExternalID != "1" || rows[0].Name != "Mjölk 3%" {
		t.Fatalf("row0 = %+v", rows[0])
	}
	if rows[0].FoodGroup != nil {
		t.Fatalf("row0 food group = %v, want nil (no groups passed)", *rows[0].FoodGroup)
	}
	if rows[0].Aisle == nil || *rows[0].Aisle != 2 {
		t.Fatalf("row0 aisle = %v, want 2 (dairy via name)", rows[0].Aisle)
	}
	if rows[1].Aisle == nil || *rows[1].Aisle != 4 {
		t.Fatalf("row1 aisle = %v, want 4 (fish via name)", rows[1].Aisle)
	}
}

func TestParseLivsmedelsverketWithGroups(t *testing.T) {
	// nummer 1 -> mapped group (Mjölk->2); nummer 2 -> unmapped group, name
	// fallback (Lax->4); nummer 3 absent from groups (name fallback).
	groups := map[int]string{
		1: "Mjölk",
		2: "Kosttillskott och hälsopreparat", // food_group set but no aisle mapping
	}
	const src = `{"livsmedel":[
		{"nummer":1,"namn":"Mjölk 3%"},
		{"nummer":2,"namn":"Lax, rökt"},
		{"nummer":4,"namn":"Knäckebröd"}
	]}`
	rows, err := ParseLivsmedelsverket(strings.NewReader(src), groups)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	// nummer 1: mapped group -> food_group set, aisle from group (2).
	if rows[0].FoodGroup == nil || *rows[0].FoodGroup != "Mjölk" {
		t.Fatalf("row0 food group = %v, want Mjölk", rows[0].FoodGroup)
	}
	if rows[0].Aisle == nil || *rows[0].Aisle != 2 {
		t.Fatalf("row0 aisle = %v, want 2 (from group)", rows[0].Aisle)
	}
	// nummer 2: group present but unmapped -> food_group set, aisle from name (Lax->4).
	if rows[1].FoodGroup == nil || *rows[1].FoodGroup != "Kosttillskott och hälsopreparat" {
		t.Fatalf("row1 food group = %v", rows[1].FoodGroup)
	}
	if rows[1].Aisle == nil || *rows[1].Aisle != 4 {
		t.Fatalf("row1 aisle = %v, want 4 (name fallback)", rows[1].Aisle)
	}
	// nummer 4: absent from groups -> no food_group, aisle from name (Knäckebröd->5).
	if rows[2].FoodGroup != nil {
		t.Fatalf("row2 food group = %v, want nil", *rows[2].FoodGroup)
	}
	if rows[2].Aisle == nil || *rows[2].Aisle != 5 {
		t.Fatalf("row2 aisle = %v, want 5 (name fallback)", rows[2].Aisle)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/catalog/ -run TestParseLivsmedelsverket -v`
Expected: compile failure — `ParseLivsmedelsverket` takes 1 arg / `Row.FoodGroup` undefined.

- [ ] **Step 3: Add `Row.FoodGroup` and persist `food_group`**

In `backend/internal/catalog/catalog.go`, add the field to `Row`:

```go
type Row struct {
	Source     string
	ExternalID string
	Name       string
	FoodGroup  *string
	Aisle      *int
}
```

Replace `upsertFoodSQL` and the `QueryRow` call in `UpsertFood`:

```go
const upsertFoodSQL = `
INSERT INTO food_catalog (source, external_id, name, food_group, aisle)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (source, external_id)
DO UPDATE SET name = EXCLUDED.name, food_group = EXCLUDED.food_group, aisle = EXCLUDED.aisle, updated_at = now()
RETURNING (xmax = 0) AS inserted`
```

```go
		if err = db.QueryRow(ctx, upsertFoodSQL, r.Source, r.ExternalID, r.Name, r.FoodGroup, r.Aisle).Scan(&wasInsert); err != nil {
```

- [ ] **Step 4: Enrich `ParseLivsmedelsverket`**

Replace `ParseLivsmedelsverket` in `backend/internal/catalog/livsmedelsverket.go`:

```go
// ParseLivsmedelsverket decodes the Livsmedelsverket products JSON into Rows.
// Items with an empty name are skipped. When groups (nummer -> EuroFIR food
// group, from ParseKlassificeringar) is non-nil, each row's FoodGroup is set and
// its aisle prefers the food-group mapping, falling back to the name heuristic.
func ParseLivsmedelsverket(r io.Reader, groups map[int]string) ([]Row, error) {
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
		row := Row{
			Source:     "livsmedelsverket",
			ExternalID: strconv.Itoa(item.Nummer),
			Name:       name,
		}
		if g, ok := groups[item.Nummer]; ok {
			group := g
			row.FoodGroup = &group
			row.Aisle = FoodGroupAisle(g)
		}
		if row.Aisle == nil {
			row.Aisle = AisleFor(name)
		}
		rows = append(rows, row)
	}
	return rows, nil
}
```

- [ ] **Step 5: Add a `food_group` round-trip to the DB test**

In `backend/internal/catalog/upsert_test.go`, add this helper near `intp` and a new test (the container setup mirrors `TestUpsertFoodIsIdempotent`; reuse the same pattern):

```go
func strp(s string) *string { return &s }

func TestUpsertFoodPersistsFoodGroup(t *testing.T) {
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
		{Source: "livsmedelsverket", ExternalID: "1", Name: "Mjölk 3%", FoodGroup: strp("Mjölk"), Aisle: intp(2)},
	}
	if _, _, err := catalog.UpsertFood(ctx, pool, rows); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	var group string
	var aisle int
	if err := pool.QueryRow(ctx,
		`SELECT food_group, aisle FROM food_catalog WHERE source=$1 AND external_id=$2`,
		"livsmedelsverket", "1").Scan(&group, &aisle); err != nil {
		t.Fatalf("select: %v", err)
	}
	if group != "Mjölk" || aisle != 2 {
		t.Fatalf("persisted (food_group=%q, aisle=%d), want (Mjölk, 2)", group, aisle)
	}

	// Re-run with a changed food group -> updated, not duplicated.
	rows[0].FoodGroup = strp("Mejeriprodukter")
	ins, upd, err := catalog.UpsertFood(ctx, pool, rows)
	if err != nil || ins != 0 || upd != 1 {
		t.Fatalf("re-upsert: ins=%d upd=%d err=%v, want 0/1/nil", ins, upd, err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT food_group FROM food_catalog WHERE source=$1 AND external_id=$2`,
		"livsmedelsverket", "1").Scan(&group); err != nil {
		t.Fatalf("select after re-run: %v", err)
	}
	if group != "Mejeriprodukter" {
		t.Fatalf("food_group after re-run = %q, want Mejeriprodukter", group)
	}
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/catalog/ -v`
Expected: PASS (parser + enrichment + table + DB round-trip; DB tests SKIP if Docker absent, which is acceptable green).

- [ ] **Step 7: Build, vet, gofmt, commit**

```bash
go build ./... && go vet ./... && gofmt -l internal/catalog/
git add internal/catalog/catalog.go internal/catalog/livsmedelsverket.go internal/catalog/livsmedelsverket_test.go internal/catalog/upsert_test.go
git commit -m "feat(catalog): persist food_group and prefer it for aisle

Row gains FoodGroup; upsertFoodSQL writes food_catalog.food_group
(idempotent on source/external_id). ParseLivsmedelsverket takes a
nummer->group map and sets FoodGroup + a food-group-preferred aisle,
falling back to the name heuristic when no group maps.

Part of #28"
```

---

### Task 3: Wire `--klass` into `cmd/seed livsmedelsverket`

**Files:**
- Modify: `backend/cmd/seed/main.go` (`seedLivsmedelsverket`: `--klass` flag + wiring)
- Modify: `AGENTS.md` (note the klassificeringar input)

**Interfaces:**
- Consumes: `catalog.ParseKlassificeringar`, `catalog.ParseLivsmedelsverket(r, groups)` (Tasks 1–2).

- [ ] **Step 1: Update `seedLivsmedelsverket`**

In `backend/cmd/seed/main.go`, replace `seedLivsmedelsverket` with:

```go
func seedLivsmedelsverket(args []string) error {
	fs := flag.NewFlagSet("livsmedelsverket", flag.ExitOnError)
	file := fs.String("file", "../data/food/livsmedelsverket_products.json", "path to livsmedelsverket products json")
	klass := fs.String("klass", "../data/food/livsmedelsverket_klassificeringar.json", "path to livsmedelsverket klassificeringar json (food groups); optional")
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

	// Food groups are optional: if the klassificeringar file is present, use it
	// to enrich food_group + aisle; otherwise fall back to name-only behavior.
	var groups map[int]string
	if kf, kerr := os.Open(*klass); kerr == nil {
		defer kf.Close()
		groups, err = catalog.ParseKlassificeringar(kf)
		if err != nil {
			return fmt.Errorf("parse klassificeringar %s: %w", *klass, err)
		}
		log.Printf("livsmedelsverket: loaded %d food groups from %s", len(groups), *klass)
	} else {
		log.Printf("livsmedelsverket: no klassificeringar at %s (%v); seeding without food groups", *klass, kerr)
	}

	rows, err := catalog.ParseLivsmedelsverket(f, groups)
	if err != nil {
		return err
	}

	ctx := context.Background()
	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	withGroup := 0
	for _, r := range rows {
		if r.FoodGroup != nil {
			withGroup++
		}
	}
	inserted, updated, err := catalog.UpsertFood(ctx, pool, rows)
	if err != nil {
		return err
	}
	log.Printf("livsmedelsverket: %d parsed (%d with food group), %d inserted, %d updated", len(rows), withGroup, inserted, updated)
	return nil
}
```

- [ ] **Step 2: Build + vet**

Run: `go build ./... && go vet ./...`
Expected: no output (the `cmd/seed` package compiles against the new signatures).

- [ ] **Step 3: Update AGENTS.md**

In `AGENTS.md`, replace the line:

```
- `go run ./cmd/seed` — import `data/food/*` into the catalog + EAN tables (one-shot, local).
```

with:

```
- `go run ./cmd/seed livsmedelsverket|openfoodfacts` — import `data/food/*` into the catalog + EAN tables (one-shot, local). `livsmedelsverket` also reads `livsmedelsverket_klassificeringar.json` (food groups) when present.
```

- [ ] **Step 4: Full suite + commit**

Run: `go test ./... && gofmt -l cmd/ internal/`
Expected: all packages ok (DB tests skip without Docker); gofmt prints nothing.

```bash
git add cmd/seed/main.go ../AGENTS.md
git commit -m "feat(seed): load food groups into livsmedelsverket seeding

Add --klass flag to cmd/seed livsmedelsverket; when the klassificeringar
file is present, enrich food_group + aisle via ParseKlassificeringar,
otherwise seed name-only. Report how many rows got a food group.

Closes #28"
```

---

## Self-Review

**Spec coverage:**
- Fetch/ingest klassificeringar; data/ gitignored, read by cmd/seed → Task 3 `--klass` ingest (fetch done as data-prep). ✓
- Populate `food_catalog.food_group` from EuroFIR group → Task 1 `ParseKlassificeringar` + Task 2 `Row.FoodGroup`/`upsertFoodSQL`. ✓
- Map group→aisle, prefer group over name, fallback to keyword → Task 1 `FoodGroupAisle` + Task 2 `ParseLivsmedelsverket` precedence. ✓
- Idempotent/re-runnable → unchanged `ON CONFLICT (source, external_id)`; Task 2 DB round-trip asserts re-run updates + changed food_group. ✓
- Reduce substring false positives by leaning on food group → group-preferred aisle (Task 2). ✓

**Placeholder scan:** none — full file contents, the complete 93-group table, and exact tests provided.

**Type consistency:** `ParseKlassificeringar`/`FoodGroupAisle` signatures match between Task 1 definition and Task 2/3 use; `Row.FoodGroup *string` consistent across catalog.go, parser, tests, upsert; `ParseLivsmedelsverket(r, groups)` updated at its sole caller (cmd/seed) and its test. `intp`/`deref`/`strp` helpers exist or are added. ✓
