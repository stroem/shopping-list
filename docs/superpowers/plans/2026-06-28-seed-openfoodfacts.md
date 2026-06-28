# Seed Open Food Facts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stream-import the ~23,473 named Swedish Open Food Facts products into `ean_mappings`, with normalized fixed-shape detail (nutrition, ingredients, allergens, labels, quantity, serving) for barcode scanning and branded-product autocomplete.

**Architecture:** A migration `0002` adds typed detail columns + four NOT-NULL JSONB columns to `ean_mappings`. The existing `internal/catalog` package gains an OFF line parser (normalizing into fixed shapes), a category→aisle mapper, and an idempotent `UpsertEAN`. `cmd/seed` gets a streaming `openfoodfacts` subcommand. Reuses #5's `AisleFor`, `Querier`, and `xmax=0` upsert pattern.

**Tech Stack:** Go 1.26 · jackc/pgx/v5 + pgxpool · encoding/json (json.Number / custom flexible numeric) · golang-migrate (embedded) · testcontainers-go.

## Global Constraints

- Go module `github.com/stroem/shopping-list/backend`; `go 1.26`.
- Idempotent on `ean_mappings (ean)` PK; same `xmax = 0` insert/update discriminator as #5's `UpsertFood`.
- Import a row only if it has a non-empty name (`product_name_sv` ?? `product_name`); `code` is present on 100% of rows and becomes `ean`.
- JSONB columns store **our normalized shapes, never raw OFF fields**; `raw` stays NULL. The four JSONB columns are `NOT NULL` with empty defaults.
- Numeric quantity/serving lifted from OFF's parsed fields; `nutriscore_grade` `"unknown"`/`"not-applicable"` → NULL; `en:`/`xx:` language prefix stripped from all tag lists.
- Aisle = `AisleForCategories(categories_tags)` ?? `AisleFor(name)` ?? NULL.
- No alcohol / non-food filter.
- DB-backed tests skip cleanly without docker. `data/` is read by path, never committed.
- Conventional Commits; **no AI attribution**; commit on `feat/issue-6-seed-openfoodfacts` only.

## File structure

- `backend/migrations/0002_ean_mappings_details.up.sql` / `.down.sql` — new columns + name trigram index.
- `backend/internal/catalog/aisle_categories.go` (+ `_test.go`) — `AisleForCategories`.
- `backend/internal/catalog/openfoodfacts.go` (+ `_test.go`) — OFF types, `flexNum`, `ParseOFFLine`, normalization.
- `backend/internal/catalog/catalog.go` (modify) — add `UpsertEAN` + `ean_mappings` insert SQL.
- `backend/internal/catalog/upsert_ean_test.go` — testcontainers idempotency.
- `backend/cmd/seed/main.go` (modify) — `openfoodfacts` streaming subcommand.

---

### Task 1: Migration `0002` — detail columns + trigram index

**Files:**
- Create: `backend/migrations/0002_ean_mappings_details.up.sql`, `backend/migrations/0002_ean_mappings_details.down.sql`

**Interfaces:**
- Produces: the columns `quantity_text, quantity_value, quantity_unit, serving_text, serving_value, nutriscore_grade, nova_group, nutriments, ingredients, allergens, labels` on `ean_mappings`, and index `ean_mappings_name_trgm`.

- [ ] **Step 1: Write `backend/migrations/0002_ean_mappings_details.up.sql`**

```sql
-- Open Food Facts product detail (issue #6). Normalized, fixed-shape columns;
-- the four jsonb columns are NOT NULL with empty defaults so the app detail page
-- never receives null. `raw` (from 0001) stays unused.
ALTER TABLE ean_mappings
    ADD COLUMN quantity_text    text,
    ADD COLUMN quantity_value   numeric,
    ADD COLUMN quantity_unit    text,
    ADD COLUMN serving_text     text,
    ADD COLUMN serving_value    numeric,
    ADD COLUMN nutriscore_grade text,
    ADD COLUMN nova_group       int,
    ADD COLUMN nutriments  jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN ingredients jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN allergens   jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN labels      jsonb NOT NULL DEFAULT '[]'::jsonb;

-- Branded-product autocomplete: trigram search over the ~23k OFF names,
-- unioned with food_catalog by the #7 endpoint.
CREATE INDEX ean_mappings_name_trgm ON ean_mappings USING gin (name gin_trgm_ops);
```

- [ ] **Step 2: Write `backend/migrations/0002_ean_mappings_details.down.sql`**

```sql
DROP INDEX IF EXISTS ean_mappings_name_trgm;

ALTER TABLE ean_mappings
    DROP COLUMN IF EXISTS quantity_text,
    DROP COLUMN IF EXISTS quantity_value,
    DROP COLUMN IF EXISTS quantity_unit,
    DROP COLUMN IF EXISTS serving_text,
    DROP COLUMN IF EXISTS serving_value,
    DROP COLUMN IF EXISTS nutriscore_grade,
    DROP COLUMN IF EXISTS nova_group,
    DROP COLUMN IF EXISTS nutriments,
    DROP COLUMN IF EXISTS ingredients,
    DROP COLUMN IF EXISTS allergens,
    DROP COLUMN IF EXISTS labels;
```

- [ ] **Step 3: Verify the up/down round-trip stays green**

The existing `internal/db` test migrates up then down and asserts table presence/absence; `//go:embed *.sql` picks up the new files automatically.

Run: `cd backend && go test ./internal/db/ -run TestMigrateUpDownRoundTrip -v`
Expected: PASS (or SKIP without docker). Migrating up applies `0002`; migrating down removes it cleanly with no error.

- [ ] **Step 4: Commit**

```bash
git add backend/migrations/0002_ean_mappings_details.up.sql backend/migrations/0002_ean_mappings_details.down.sql
git commit -m "feat(migrations): ean_mappings product detail columns"
```

---

### Task 2: `AisleForCategories` (TDD)

**Files:**
- Create: `backend/internal/catalog/aisle_categories.go`, `backend/internal/catalog/aisle_categories_test.go`

**Interfaces:**
- Consumes: nothing new (sibling of #5's `AisleFor`).
- Produces: `func AisleForCategories(tags []string) *int` — maps OFF English `categories_tags` to the 9-aisle taxonomy, first match by priority, nil if none.

- [ ] **Step 1: Write the failing test**

`backend/internal/catalog/aisle_categories_test.go`:

```go
package catalog

import "testing"

func TestAisleForCategories(t *testing.T) {
	cases := []struct {
		name string
		tags []string
		want *int
	}{
		{"dairy", []string{"en:dairies", "en:cheeses"}, intp(2)},
		{"seafood beats dairy", []string{"en:dairies", "en:seafood"}, intp(4)},
		{"meat", []string{"en:meats", "en:hams"}, intp(3)},
		{"produce", []string{"en:fresh-vegetables"}, intp(1)},
		{"bread", []string{"en:breads"}, intp(5)},
		{"frozen", []string{"en:frozen-desserts"}, intp(7)},
		{"drink incl alcohol", []string{"en:alcoholic-beverages"}, intp(8)},
		{"candy", []string{"en:chocolates"}, intp(9)},
		{"pantry catch-all", []string{"en:groceries", "en:sauces"}, intp(6)},
		{"no match", []string{"en:made-in-sweden"}, nil},
		{"empty", nil, nil},
	}
	for _, c := range cases {
		got := AisleForCategories(c.tags)
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Errorf("%s: AisleForCategories(%v) = %v, want %v", c.name, c.tags, deref(got), deref(c.want))
		}
	}
}
```

(`intp` and `deref` already exist in `aisle_test.go`, same package.)

- [ ] **Step 2: Run — expect FAIL** (`undefined: AisleForCategories`). `cd backend && go test ./internal/catalog/ -run TestAisleForCategories`

- [ ] **Step 3: Implement `backend/internal/catalog/aisle_categories.go`**

```go
package catalog

import "strings"

// categoryGroups maps OFF English category-tag substrings to the v1 aisle
// taxonomy, same priority order as AisleFor: specific proteins first, pantry
// last as the catch-all. Tags look like "en:dairies", "en:barbecue-sauces".
var categoryGroups = []aisleGroup{
	{4, []string{"seafood", "fish", "salmon", "tuna", "shellfish", "shrimp", "herring", "mackerel"}},
	{3, []string{"meat", "poultry", "beef", "pork", "chicken", "ham", "sausage", "bacon", "charcuterie", "turkey"}},
	{2, []string{"dairies", "dairy", "milk", "cheese", "yogurt", "yoghurt", "butter", "cream", "egg"}},
	{1, []string{"fruit", "vegetable", "legume", "salad", "potato", "berries", "mushroom"}},
	{5, []string{"bread", "bakery", "viennoiserie", "baguette", "toast", "crackers"}},
	{7, []string{"frozen", "ice-cream", "ice-creams"}},
	{8, []string{"beverage", "water", "juice", "soda", "coffee", "tea", "drink", "wine", "beer", "alcoholic", "spirit"}},
	{9, []string{"chocolate", "candy", "candies", "snack", "biscuit", "chips", "confectioner", "sweet", "dessert"}},
	{6, []string{"pasta", "rice", "cereal", "flour", "condiment", "sauce", "canned", "spice", "oil", "groceries", "breakfast", "legumes-and-their-products"}},
}

// AisleForCategories returns the first aisle whose keyword is a substring of any
// category tag, by taxonomy priority, or nil when nothing matches.
func AisleForCategories(tags []string) *int {
	for _, g := range categoryGroups {
		for _, kw := range g.keywords {
			for _, tag := range tags {
				if strings.Contains(strings.ToLower(tag), kw) {
					a := g.aisle
					return &a
				}
			}
		}
	}
	return nil
}
```

Note: `dessert` is under candy (9) but `frozen-desserts` matches `frozen` (7) first due to priority — that ordering is intentional and asserted by the test.

- [ ] **Step 4: Run — expect PASS.** `cd backend && go test ./internal/catalog/ -run TestAisleForCategories && go vet ./internal/catalog/`

- [ ] **Step 5: Commit**

```bash
git add backend/internal/catalog/aisle_categories.go backend/internal/catalog/aisle_categories_test.go
git commit -m "feat(catalog): map OFF category tags to aisle"
```

---

### Task 3: OFF types + `ParseOFFLine` (TDD)

**Files:**
- Create: `backend/internal/catalog/openfoodfacts.go`, `backend/internal/catalog/openfoodfacts_test.go`

**Interfaces:**
- Consumes: `AisleForCategories`, `AisleFor`.
- Produces:
  - `type Nutriments struct{ ... *float64 json:energy_kcal,fat,saturated_fat,carbohydrates,sugars,fiber,proteins,salt }`
  - `type Ingredients struct{ Text *string json:text; List []string json:list }`
  - `type EanRow struct{ ... }` (fields per the spec mapping table)
  - `func ParseOFFLine(line []byte) (EanRow, bool, error)` — `ok=false` means skip (empty name); `err != nil` only on malformed JSON.

- [ ] **Step 1: Write the failing test**

`backend/internal/catalog/openfoodfacts_test.go`:

```go
package catalog

import "testing"

const offMilk = `{"code":"7310865004703","product_name":"Milk","product_name_sv":"Mellanmjölk",
"brands":"Arla","image_url":"http://img/milk.jpg",
"quantity":"1 L","product_quantity":"1000","product_quantity_unit":"ml",
"serving_size":"2 dl (200 ml)","serving_quantity":200,
"nutriscore_grade":"b","nova_group":1,
"categories_tags":["en:dairies","en:milks"],
"ingredients_text":"Milk","ingredients_tags":["en:milk"],
"allergens_tags":["en:milk"],"labels_tags":["en:organic"],
"nutriments":{"energy-kcal_100g":47,"fat_100g":1.5,"sugars_100g":"4.8","salt_100g":0.1}}`

const offNoName = `{"code":"123","product_name":"","product_name_sv":"  "}`

const offUnknownNutri = `{"code":"9","product_name":"Mystery","nutriscore_grade":"unknown",
"product_quantity":0,"categories_tags":["en:groceries"]}`

func TestParseOFFLine_Milk(t *testing.T) {
	row, ok, err := ParseOFFLine([]byte(offMilk))
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v, want true/nil", ok, err)
	}
	if row.EAN != "7310865004703" || row.Source != "openfoodfacts" {
		t.Fatalf("ean/source = %q/%q", row.EAN, row.Source)
	}
	if row.Name != "Mellanmjölk" { // Swedish name preferred
		t.Fatalf("name = %q, want Mellanmjölk", row.Name)
	}
	if row.Brand == nil || *row.Brand != "Arla" {
		t.Fatalf("brand = %v", row.Brand)
	}
	if row.QuantityValue == nil || *row.QuantityValue != 1000 || row.QuantityUnit == nil || *row.QuantityUnit != "ml" {
		t.Fatalf("quantity = %v %v (text %v)", row.QuantityValue, row.QuantityUnit, row.QuantityText)
	}
	if row.ServingValue == nil || *row.ServingValue != 200 {
		t.Fatalf("serving = %v", row.ServingValue)
	}
	if row.NutriscoreGrade == nil || *row.NutriscoreGrade != "b" {
		t.Fatalf("nutriscore = %v", row.NutriscoreGrade)
	}
	if row.NovaGroup == nil || *row.NovaGroup != 1 {
		t.Fatalf("nova = %v", row.NovaGroup)
	}
	if row.Aisle == nil || *row.Aisle != 2 { // en:dairies
		t.Fatalf("aisle = %v, want 2", row.Aisle)
	}
	if row.Nutriments.Fat == nil || *row.Nutriments.Fat != 1.5 {
		t.Fatalf("fat = %v", row.Nutriments.Fat)
	}
	if row.Nutriments.Sugars == nil || *row.Nutriments.Sugars != 4.8 { // string "4.8" coerced
		t.Fatalf("sugars = %v", row.Nutriments.Sugars)
	}
	if row.Nutriments.Proteins != nil { // absent → nil in fixed shape
		t.Fatalf("proteins = %v, want nil", row.Nutriments.Proteins)
	}
	if len(row.Allergens) != 1 || row.Allergens[0] != "milk" { // en: stripped
		t.Fatalf("allergens = %v", row.Allergens)
	}
	if len(row.Labels) != 1 || row.Labels[0] != "organic" {
		t.Fatalf("labels = %v", row.Labels)
	}
	if row.Ingredients.Text == nil || *row.Ingredients.Text != "Milk" ||
		len(row.Ingredients.List) != 1 || row.Ingredients.List[0] != "milk" {
		t.Fatalf("ingredients = %+v", row.Ingredients)
	}
}

func TestParseOFFLine_SkipNoName(t *testing.T) {
	_, ok, err := ParseOFFLine([]byte(offNoName))
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v, want false/nil (skip)", ok, err)
	}
}

func TestParseOFFLine_UnknownAndFallback(t *testing.T) {
	row, ok, err := ParseOFFLine([]byte(offUnknownNutri))
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if row.NutriscoreGrade != nil { // "unknown" → nil
		t.Fatalf("nutriscore = %v, want nil", row.NutriscoreGrade)
	}
	if row.QuantityValue == nil || *row.QuantityValue != 0 { // numeric 0 parses
		t.Fatalf("quantity_value = %v, want 0", row.QuantityValue)
	}
	if row.Allergens == nil || len(row.Allergens) != 0 { // never nil, empty slice
		t.Fatalf("allergens = %v, want []", row.Allergens)
	}
	if row.Aisle == nil || *row.Aisle != 6 { // en:groceries → pantry
		t.Fatalf("aisle = %v, want 6", row.Aisle)
	}
}

func TestParseOFFLine_Malformed(t *testing.T) {
	if _, _, err := ParseOFFLine([]byte(`{not json`)); err == nil {
		t.Fatal("want error on malformed JSON")
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (`undefined: ParseOFFLine`). `cd backend && go test ./internal/catalog/ -run TestParseOFFLine`

- [ ] **Step 3: Implement `backend/internal/catalog/openfoodfacts.go`**

```go
package catalog

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Nutriments is the fixed per-100g nutrition shape; every key always present,
// value a number or null.
type Nutriments struct {
	EnergyKcal    *float64 `json:"energy_kcal"`
	Fat           *float64 `json:"fat"`
	SaturatedFat  *float64 `json:"saturated_fat"`
	Carbohydrates *float64 `json:"carbohydrates"`
	Sugars        *float64 `json:"sugars"`
	Fiber         *float64 `json:"fiber"`
	Proteins      *float64 `json:"proteins"`
	Salt          *float64 `json:"salt"`
}

// Ingredients is the fixed ingredients shape: free text plus a token list.
type Ingredients struct {
	Text *string  `json:"text"`
	List []string `json:"list"`
}

// EanRow is one ean_mappings record to upsert.
type EanRow struct {
	EAN             string
	Name            string
	Brand           *string
	ImageURL        *string
	QuantityText    *string
	QuantityValue   *float64
	QuantityUnit    *string
	ServingText     *string
	ServingValue    *float64
	NutriscoreGrade *string
	NovaGroup       *int
	Nutriments      Nutriments
	Ingredients     Ingredients
	Allergens       []string
	Labels          []string
	Aisle           *int
	Source          string
}

// flexNum tolerates OFF's mixed encoding: a field may arrive as a JSON number
// (7788.0) or a JSON string ("0"). Empty/null/garbage leaves it unset.
type flexNum struct {
	set bool
	val float64
}

func (f *flexNum) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil // tolerate junk → unset, never fail the whole line
	}
	f.set, f.val = true, v
	return nil
}

func (f flexNum) floatPtr() *float64 {
	if !f.set {
		return nil
	}
	v := f.val
	return &v
}

type offProduct struct {
	Code                string             `json:"code"`
	ProductName         string             `json:"product_name"`
	ProductNameSv       string             `json:"product_name_sv"`
	Brands              string             `json:"brands"`
	ImageURL            string             `json:"image_url"`
	ImageSmallURL       string             `json:"image_small_url"`
	Quantity            string             `json:"quantity"`
	ProductQuantity     flexNum            `json:"product_quantity"`
	ProductQuantityUnit string             `json:"product_quantity_unit"`
	ServingSize         string             `json:"serving_size"`
	ServingQuantity     flexNum            `json:"serving_quantity"`
	NutriscoreGrade     string             `json:"nutriscore_grade"`
	NovaGroup           flexNum            `json:"nova_group"`
	CategoriesTags      []string           `json:"categories_tags"`
	IngredientsText     string             `json:"ingredients_text"`
	IngredientsTags     []string           `json:"ingredients_tags"`
	AllergensTags       []string           `json:"allergens_tags"`
	LabelsTags          []string           `json:"labels_tags"`
	Nutriments          map[string]flexNum `json:"nutriments"`
}

// ParseOFFLine decodes one Open Food Facts JSONL line into a normalized EanRow.
// Returns ok=false to signal a skip (empty name); err only on malformed JSON.
func ParseOFFLine(line []byte) (EanRow, bool, error) {
	var p offProduct
	if err := json.Unmarshal(line, &p); err != nil {
		return EanRow{}, false, err
	}

	name := strings.TrimSpace(p.ProductNameSv)
	if name == "" {
		name = strings.TrimSpace(p.ProductName)
	}
	if name == "" {
		return EanRow{}, false, nil
	}

	row := EanRow{
		EAN:          strings.TrimSpace(p.Code),
		Name:         name,
		Brand:        strp(p.Brands),
		ImageURL:     strp(firstNonEmpty(p.ImageURL, p.ImageSmallURL)),
		QuantityText: strp(p.Quantity),
		ServingText:  strp(p.ServingSize),
		Source:       "openfoodfacts",
		Allergens:    stripPrefixAll(p.AllergensTags),
		Labels:       stripPrefixAll(p.LabelsTags),
		Ingredients: Ingredients{
			Text: strp(p.IngredientsText),
			List: stripPrefixAll(p.IngredientsTags),
		},
		Nutriments: Nutriments{
			EnergyKcal:    p.Nutriments["energy-kcal_100g"].floatPtr(),
			Fat:           p.Nutriments["fat_100g"].floatPtr(),
			SaturatedFat:  p.Nutriments["saturated-fat_100g"].floatPtr(),
			Carbohydrates: p.Nutriments["carbohydrates_100g"].floatPtr(),
			Sugars:        p.Nutriments["sugars_100g"].floatPtr(),
			Fiber:         p.Nutriments["fiber_100g"].floatPtr(),
			Proteins:      p.Nutriments["proteins_100g"].floatPtr(),
			Salt:          p.Nutriments["salt_100g"].floatPtr(),
		},
		QuantityValue: p.ProductQuantity.floatPtr(),
		QuantityUnit:  normUnit(p.ProductQuantityUnit),
		ServingValue:  p.ServingQuantity.floatPtr(),
		NutriscoreGrade: normGrade(p.NutriscoreGrade),
		NovaGroup:     normNova(p.NovaGroup),
	}
	if a := AisleForCategories(p.CategoriesTags); a != nil {
		row.Aisle = a
	} else {
		row.Aisle = AisleFor(name)
	}
	return row, true, nil
}

func strp(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// stripPrefixAll removes the leading "<lang>:" (e.g. "en:", "xx:") from each tag
// and returns a non-nil slice (so JSON marshals to [] not null).
func stripPrefixAll(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if i := strings.IndexByte(t, ':'); i >= 0 {
			t = t[i+1:]
		}
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

func normUnit(u string) *string {
	u = strings.ToLower(strings.TrimSpace(u))
	if u == "g" || u == "ml" {
		return &u
	}
	return nil
}

func normGrade(g string) *string {
	g = strings.ToLower(strings.TrimSpace(g))
	switch g {
	case "a", "b", "c", "d", "e":
		return &g
	default:
		return nil
	}
}

func normNova(n flexNum) *int {
	if !n.set {
		return nil
	}
	i := int(n.val)
	if i < 1 || i > 4 {
		return nil
	}
	return &i
}
```

- [ ] **Step 4: Run — expect PASS.** `cd backend && go test ./internal/catalog/ -run TestParseOFFLine && go vet ./internal/catalog/`

- [ ] **Step 5: Commit**

```bash
git add backend/internal/catalog/openfoodfacts.go backend/internal/catalog/openfoodfacts_test.go
git commit -m "feat(catalog): parse and normalize open food facts lines"
```

---

### Task 4: `UpsertEAN` (testcontainers)

**Files:**
- Modify: `backend/internal/catalog/catalog.go`
- Create: `backend/internal/catalog/upsert_ean_test.go`

**Interfaces:**
- Consumes: `EanRow`, `Querier` (#5), migration `0002`.
- Produces: `func UpsertEAN(ctx context.Context, db Querier, rows []EanRow) (inserted, updated int, err error)`.

- [ ] **Step 1: Add `UpsertEAN` to `backend/internal/catalog/catalog.go`**

Add `"encoding/json"` to the import block, then append:

```go
const upsertEANSQL = `
INSERT INTO ean_mappings (
    ean, name, brand, aisle, image_url, source,
    quantity_text, quantity_value, quantity_unit,
    serving_text, serving_value, nutriscore_grade, nova_group,
    nutriments, ingredients, allergens, labels
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9,
    $10, $11, $12, $13,
    $14, $15, $16, $17
)
ON CONFLICT (ean) DO UPDATE SET
    name = EXCLUDED.name, brand = EXCLUDED.brand, aisle = EXCLUDED.aisle,
    image_url = EXCLUDED.image_url, source = EXCLUDED.source,
    quantity_text = EXCLUDED.quantity_text, quantity_value = EXCLUDED.quantity_value,
    quantity_unit = EXCLUDED.quantity_unit, serving_text = EXCLUDED.serving_text,
    serving_value = EXCLUDED.serving_value, nutriscore_grade = EXCLUDED.nutriscore_grade,
    nova_group = EXCLUDED.nova_group, nutriments = EXCLUDED.nutriments,
    ingredients = EXCLUDED.ingredients, allergens = EXCLUDED.allergens,
    labels = EXCLUDED.labels, updated_at = now()
RETURNING (xmax = 0) AS inserted`

// UpsertEAN inserts or updates each OFF product idempotently, keyed by ean.
// Returns how many rows were newly inserted vs updated (xmax = 0 ⇒ insert).
func UpsertEAN(ctx context.Context, db Querier, rows []EanRow) (inserted, updated int, err error) {
	for _, r := range rows {
		nutriments, _ := json.Marshal(r.Nutriments)
		ingredients, _ := json.Marshal(r.Ingredients)
		allergens, _ := json.Marshal(r.Allergens)
		labels, _ := json.Marshal(r.Labels)

		var wasInsert bool
		err = db.QueryRow(ctx, upsertEANSQL,
			r.EAN, r.Name, r.Brand, r.Aisle, r.ImageURL, r.Source,
			r.QuantityText, r.QuantityValue, r.QuantityUnit,
			r.ServingText, r.ServingValue, r.NutriscoreGrade, r.NovaGroup,
			nutriments, ingredients, allergens, labels,
		).Scan(&wasInsert)
		if err != nil {
			return inserted, updated, fmt.Errorf("upsert ean %s: %w", r.EAN, err)
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

(`json.Marshal` of the four fields yields `[]byte`; pgx's jsonb codec writes pre-encoded JSON bytes directly. `Allergens`/`Labels`/`Ingredients.List` are non-nil empty slices from the parser, so they marshal to `[]`, satisfying the NOT NULL columns.)

- [ ] **Step 2: Write `backend/internal/catalog/upsert_ean_test.go`**

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

func strpt(s string) *string { return &s }

func TestUpsertEANIsIdempotent(t *testing.T) {
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

	rows := []catalog.EanRow{
		{EAN: "111", Name: "Mellanmjölk", Source: "openfoodfacts", Aisle: intp(2),
			NutriscoreGrade: strpt("b"), Allergens: []string{"milk"}, Labels: []string{},
			Nutriments: catalog.Nutriments{}, Ingredients: catalog.Ingredients{List: []string{}}},
		{EAN: "222", Name: "Lax", Source: "openfoodfacts", Aisle: intp(4),
			Allergens: []string{"fish"}, Labels: []string{},
			Nutriments: catalog.Nutriments{}, Ingredients: catalog.Ingredients{List: []string{}}},
	}

	ins, upd, err := catalog.UpsertEAN(ctx, pool, rows)
	if err != nil || ins != 2 || upd != 0 {
		t.Fatalf("first upsert: ins=%d upd=%d err=%v, want 2/0/nil", ins, upd, err)
	}

	ins, upd, err = catalog.UpsertEAN(ctx, pool, rows)
	if err != nil || ins != 0 || upd != 2 {
		t.Fatalf("second upsert: ins=%d upd=%d err=%v, want 0/2/nil", ins, upd, err)
	}

	// Changed field updates in place; row count stays 1; jsonb round-trips.
	rows[0].NutriscoreGrade = strpt("c")
	if _, _, err := catalog.UpsertEAN(ctx, pool, rows); err != nil {
		t.Fatalf("third upsert: %v", err)
	}
	var count int
	var grade string
	var allergens []byte
	if err := pool.QueryRow(ctx,
		`SELECT count(*) OVER (), nutriscore_grade, allergens FROM ean_mappings WHERE ean='111'`,
	).Scan(&count, &grade, &allergens); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if count != 1 || grade != "c" || string(allergens) != `["milk"]` {
		t.Fatalf("after update: count=%d grade=%q allergens=%s", count, grade, allergens)
	}
}
```

- [ ] **Step 3: Run the catalog suite + vet**

Run: `cd backend && go test ./internal/catalog/ && go vet ./internal/catalog/`
Expected: aisle/category/parse pass; the two upsert tests pass with docker (or skip).

- [ ] **Step 4: Commit**

```bash
git add backend/internal/catalog/catalog.go backend/internal/catalog/upsert_ean_test.go
git commit -m "feat(catalog): idempotent ean_mappings upsert"
```

---

### Task 5: `cmd/seed openfoodfacts` streaming subcommand

**Files:**
- Modify: `backend/cmd/seed/main.go`

**Interfaces:**
- Consumes: `config.Load`, `db.NewPool`, `catalog.ParseOFFLine`, `catalog.UpsertEAN`.

- [ ] **Step 1: Extend `backend/cmd/seed/main.go`**

Add the `openfoodfacts` case to the dispatch switch and the usage line, and add the streaming handler. Update imports to include `bufio`, `errors`, `io`.

Replace the `switch` and `usage` in `main()`:

```go
	switch os.Args[1] {
	case "livsmedelsverket":
		if err := seedLivsmedelsverket(os.Args[2:]); err != nil {
			log.Fatalf("seed livsmedelsverket: %v", err)
		}
	case "openfoodfacts":
		if err := seedOpenFoodFacts(os.Args[2:]); err != nil {
			log.Fatalf("seed openfoodfacts: %v", err)
		}
	default:
		usage()
	}
```

```go
func usage() {
	fmt.Fprintln(os.Stderr, "usage: seed <livsmedelsverket|openfoodfacts> [--file <path>]")
	os.Exit(2)
}
```

Add the handler:

```go
func seedOpenFoodFacts(args []string) error {
	fs := flag.NewFlagSet("openfoodfacts", flag.ExitOnError)
	file := fs.String("file", "../data/food/swedish_food_products.jsonl", "path to open food facts jsonl")
	batchSize := fs.Int("batch", 500, "rows per upsert batch")
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

	ctx := context.Background()
	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// bufio.Reader (not Scanner): some OFF lines exceed Scanner's 64 KB cap.
	r := bufio.NewReaderSize(f, 1<<20)
	var (
		parsed, skipped, malformed, inserted, updated int
		batch                                         []catalog.EanRow
	)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		ins, upd, err := catalog.UpsertEAN(ctx, pool, batch)
		if err != nil {
			return err
		}
		inserted += ins
		updated += upd
		batch = batch[:0]
		log.Printf("openfoodfacts: progress — %d parsed, %d inserted, %d updated", parsed, inserted, updated)
		return nil
	}

	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			row, ok, perr := catalog.ParseOFFLine(line)
			switch {
			case perr != nil:
				malformed++
			case !ok:
				skipped++
			default:
				parsed++
				batch = append(batch, row)
				if len(batch) >= *batchSize {
					if ferr := flush(); ferr != nil {
						return ferr
					}
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read %s: %w", *file, err)
		}
	}
	if ferr := flush(); ferr != nil {
		return ferr
	}

	log.Printf("openfoodfacts: done — %d parsed, %d skipped, %d malformed, %d inserted, %d updated",
		parsed, skipped, malformed, inserted, updated)
	return nil
}
```

(If `errors` ends up unused after editing, drop it — only `bufio`, `io` are strictly required additions here. Verify with `go vet`.)

- [ ] **Step 2: Build, vet, full test**

Run: `cd backend && go build ./... && go vet ./... && go test ./...`
Expected: all green (catalog + db tests pass/skip; nothing else affected).

- [ ] **Step 3: Commit**

```bash
git add backend/cmd/seed/main.go
git commit -m "feat(seed): stream open food facts into ean_mappings"
```

---

### Task 6: Real end-to-end verification (no new code)

**Files:** none (verification only; uses gitignored `data/`).

- [ ] **Step 1: Start a local Postgres, migrate, seed the real file**

```bash
cid=$(podman run -d --rm -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=shopping_list -p 55432:5432 postgres:16-alpine)
sleep 4
export DATABASE_URL="postgres://postgres:postgres@localhost:55432/shopping_list?sslmode=disable"
cd backend
go run ./cmd/migrate up
go run ./cmd/seed openfoodfacts --file /home/stroem/code/github/stroem/shopping-list/data/food/swedish_food_products.jsonl
```

Expected: `done — ~23473 parsed, ~1467 skipped, 0 malformed, ~23473 inserted, 0 updated`.

- [ ] **Step 2: Verify idempotency + spot-check normalized detail**

```bash
go run ./cmd/seed openfoodfacts --file /home/stroem/code/github/stroem/shopping-list/data/food/swedish_food_products.jsonl
# expect: 0 inserted, ~23473 updated
PGPASSWORD=postgres psql -h localhost -p 55432 -U postgres -d shopping_list -c \
  "SELECT count(*) total, count(aisle) with_aisle, count(nutriscore_grade) with_grade FROM ean_mappings;"
PGPASSWORD=postgres psql -h localhost -p 55432 -U postgres -d shopping_list -c \
  "SELECT ean, name, aisle, quantity_value, quantity_unit, nutriscore_grade, nutriments, allergens FROM ean_mappings WHERE jsonb_array_length(allergens) > 0 LIMIT 3;"
```

Expected: total ≈ 23,473; `allergens`/`labels` are arrays (never null); `nutriments` is the fixed object shape; aisle populated for a sizable share.

- [ ] **Step 3: Stop the container**

```bash
podman stop "$cid"
```

No commit (verification only).

---

## Self-Review

**Spec coverage:**
- Migration `0002` (columns + NOT NULL JSONB defaults + name trigram) → Task 1. ✓
- Normalized fixed-shape JSONB (nutriments per-100g, ingredients, allergens, labels) → Task 3 types + `ParseOFFLine`. ✓
- Quantity/serving value+unit+text lift; nutriscore `unknown`→null; nova 1–4; `en:` strip → Task 3 `normUnit`/`normGrade`/`normNova`/`stripPrefixAll` + tests. ✓
- Aisle = categories → name → null → Task 2 + `ParseOFFLine`. ✓
- Require name, skip nameless; keyed by ean; no alcohol filter → Task 3 (`ok=false`), Task 5. ✓
- Idempotent `xmax=0` upsert → Task 4. ✓
- Streaming (bufio.Reader, batched), malformed-line tolerance → Task 5. ✓
- Real end-to-end run → Task 6. ✓
- `raw` stays NULL; CHECK/churn-guard/GIN deferred → not implemented (correct per spec). ✓

**Placeholder scan:** No TBD/TODO; every code block complete. The one conditional note (drop `errors` import if unused) is a concrete vet-driven instruction, not a placeholder.

**Type consistency:** `EanRow`/`Nutriments`/`Ingredients` fields identical across Task 3 (definition), Task 4 (`UpsertEAN` + test), and Task 5 (batch). `ParseOFFLine(line []byte) (EanRow, bool, error)` and `UpsertEAN(ctx, Querier, []EanRow) (int,int,error)` consistent at all call sites. `AisleForCategories(tags []string) *int` matches Task 2 and its `ParseOFFLine` caller. `flexNum.floatPtr()`/`normNova` return the pointer types the struct fields declare. `Querier` reused from #5 unchanged.
