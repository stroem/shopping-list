# Seed Open Food Facts into ean_mappings (issue #6) — design

**Status:** approved 2026-06-28
**Issue:** [#6 — Seed Open Food Facts EAN mappings into ean_mappings](https://github.com/stroem/shopping-list/issues/6)
(milestone *M1 — Food catalog & data pipeline*)
**Builds on:** #2 (`ean_mappings` table), #3 (`config`, `db.NewPool`),
#5 (`internal/catalog` package, `AisleFor`, the 9-aisle taxonomy, the idempotent
`xmax=0` upsert pattern).

## Goal

Make `cmd/seed` stream-import the ~24,940 Swedish Open Food Facts products from
`data/food/swedish_food_products.jsonl` (~434 MB) into `ean_mappings`, for both
**barcode scanning** (EAN → product) and **branded-product autocomplete**.
Importantly, store **rich, normalized product detail** (nutrition, ingredients,
allergens, labels, quantity, serving) in a **fixed, predictable shape** so the
app's product detail page can rely on the same structure for every row.

## Data reality (drives the design)

Measured on the real file (24,940 rows):

- **`code` is present on 100%** of rows — every product has an EAN, so keying
  `ean_mappings` by `ean` is safe. The "EAN-less product" case (scan a barcode
  that isn't in the DB → user adds it manually → we store it) is a **runtime
  concern handled by a future write endpoint**, not by this seed.
- **23,473 rows have both a code and a name**; 1,467 have a code but no usable
  name. A nameless row is useless for autocomplete and for a detail page, so we
  **require a name** and skip the rest.
- OFF already provides **parsed numeric quantities**: `product_quantity` (number)
  + `product_quantity_unit` (**only ever `g` or `ml`** — normalized to base unit),
  alongside the messy free-text `quantity` ("6 x 1,298 kg", "18 oz"). Likewise
  `serving_quantity` (number) + `serving_size` text. We **lift OFF's parsed
  numerics** rather than parse the text ourselves.
- `nutriscore_grade` is present on 99% but is frequently the literal `"unknown"`
  (also `"not-applicable"`); these map to **NULL**.
- Each record has ~130 keys, most of them OFF-internal (editor/debug/version
  metadata). We deliberately do **not** store the raw record.

## Scope decisions

- **No alcohol / non-food filter.** Per the product owner, alcoholic products are
  fine to store — they are catalog data and many are sold in ordinary stores.
  This intentionally relaxes the AGENTS.md "no alcohol" line **for stored catalog
  data only** (we are not building Systembolaget features). The only skip rule is
  "must have a name".
- **JSONB holds our own normalized shapes, never raw OFF fields.** Raw OFF data
  varies wildly row to row; a detail page cannot parse that. Every JSONB column
  has a fixed schema, the same for every row.
- The existing `ean_mappings.raw` column is **left unused (NULL)** — no arbitrary
  blobs.

## Schema — migration `0002_ean_mappings_details`

`ean_mappings` already has: `ean` (PK), `name`, `brand`, `aisle`, `image_url`,
`source`, `raw`, `created_at`, `updated_at`, `deleted_at` (from #2).

`0002` **adds** (all nullable unless stated):

```sql
ALTER TABLE ean_mappings
  ADD COLUMN quantity_text    text,
  ADD COLUMN quantity_value   numeric,
  ADD COLUMN quantity_unit    text,                       -- 'g' | 'ml'
  ADD COLUMN serving_text     text,
  ADD COLUMN serving_value    numeric,
  ADD COLUMN nutriscore_grade text,                       -- 'a'..'e' | NULL
  ADD COLUMN nova_group       int,                        -- 1..4 | NULL
  ADD COLUMN nutriments  jsonb NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN ingredients jsonb NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN allergens   jsonb NOT NULL DEFAULT '[]'::jsonb,
  ADD COLUMN labels      jsonb NOT NULL DEFAULT '[]'::jsonb;

CREATE INDEX ean_mappings_name_trgm ON ean_mappings USING gin (name gin_trgm_ops);
```

**Improvement A (chosen):** the four JSONB columns are `NOT NULL` with empty
defaults, so a detail page never receives `null` for them — always an object or
an array. (CHECK constraints, a churn-free upsert guard, and JSONB GIN indexes
were considered and **deferred** — YAGNI for now.)

The `name` trigram GIN index lets the #7 autocomplete endpoint search the ~23k
branded products here, unioned with the generic `food_catalog` from #5.

## Normalized JSONB shapes (identical for every row)

```jsonc
// nutriments — per 100 g/ml; each value a number or null
nutriments = {
  "energy_kcal": 380, "fat": 12.0, "saturated_fat": 3.0,
  "carbohydrates": 60.0, "sugars": 22.0, "fiber": 2.0,
  "proteins": 7.0, "salt": 1.1
}
// ingredients — free text + a token list
ingredients = { "text": "Wheat flour, sugar, …" | null, "list": ["wheat-flour","sugar"] }
// allergens / labels — string arrays, "en:" language prefix stripped, [] if none
allergens = ["milk","gluten"]
labels    = ["organic","vegan"]
```

Mapping from OFF source fields:

| our field            | OFF source                                   | normalization |
|----------------------|----------------------------------------------|---------------|
| `name`               | `product_name_sv` ?? `product_name`          | trimmed; skip row if empty |
| `brand`              | `brands`                                     | trimmed; NULL if empty |
| `image_url`          | `image_url` ?? `image_small_url`             | NULL if empty |
| `quantity_text`      | `quantity`                                   | as-is |
| `quantity_value`     | `product_quantity`                           | parsed number; NULL if unparseable |
| `quantity_unit`      | `product_quantity_unit`                      | `g`/`ml`; NULL otherwise |
| `serving_text`       | `serving_size`                               | as-is |
| `serving_value`      | `serving_quantity`                           | parsed number |
| `nutriscore_grade`   | `nutriscore_grade`                           | lowercased; NULL unless one of a–e |
| `nova_group`         | `nova_group`                                 | int 1–4; NULL otherwise |
| `nutriments.*`       | `nutriments.{energy-kcal_100g,fat_100g,saturated-fat_100g,carbohydrates_100g,sugars_100g,fiber_100g,proteins_100g,salt_100g}` | number or null |
| `ingredients.text`   | `ingredients_text`                           | NULL if empty |
| `ingredients.list`   | `ingredients_tags`                           | "en:" stripped; [] if none |
| `allergens`          | `allergens_tags`                             | "en:" stripped; [] if none |
| `labels`             | `labels_tags`                                | "en:" stripped; [] if none |
| `aisle`              | see below                                    | int 1–9 or NULL |
| `source`             | constant                                     | `"openfoodfacts"` |

## Aisle derivation

`aisle = AisleForCategories(categories_tags) ?? AisleFor(name) ?? NULL`

- **`AisleForCategories([]string) *int`** (new, `aisle_categories.go`): walks the
  OFF `categories_tags` (English, hierarchical, e.g. `en:dairies`,
  `en:barbecue-sauces`) and maps to the 9-aisle taxonomy by keyword substring,
  same priority order as #5 (fish → meat → dairy → produce → bread → frozen →
  drink → candy → pantry-as-catch-all). Representative keywords per aisle:
  - 4 Fisk: `seafood`, `fish`, `salmon`, `tuna`, `shellfish`, `shrimp`
  - 3 Kött: `meat`, `poultry`, `beef`, `pork`, `chicken`, `ham`, `sausage`, `charcuterie`
  - 2 Mejeri: `dairies`, `milk`, `cheese`, `yogurt`, `butter`, `cream`, `eggs`
  - 1 Frukt&grönt: `fruit`, `vegetable`, `legume`, `salad`, `potato`
  - 5 Bröd: `bread`, `bakery`, `viennoiserie`, `baguette`, `toast`
  - 7 Fryst: `frozen`, `ice-cream`
  - 8 Dryck: `beverage`, `water`, `juice`, `soda`, `coffee`, `tea`, `drink`, `wine`, `beer`, `alcoholic`
  - 9 Godis&snacks: `chocolate`, `candy`, `candies`, `snack`, `biscuit`, `chips`, `confectioner`, `sweet`
  - 6 Skafferi (last, catch-all): `pasta`, `rice`, `cereal`, `flour`, `condiment`, `sauce`, `canned`, `spice`, `oil`, `groceries`, `breakfast`
- Falls back to #5's name-based `AisleFor` when no category tag matches, then NULL.

## Components (all in `backend/internal/catalog`, reusing #5)

### `openfoodfacts.go`
- `type EanRow struct` — the mapped fields from the table above; JSONB fields are
  typed Go structs (`Nutriments`, `Ingredients`) / slices so they marshal to the
  fixed shape.
- `ParseOFFLine(line []byte) (EanRow, bool, error)` — decode one JSONL object,
  return `(row, false, nil)` to signal "skip" (empty name), `(_, _, err)` only on
  malformed JSON. Performs all normalization (name/brand/image pick, quantity &
  serving lift, nutriscore/nova validation, "en:" stripping, aisle derivation).

### `aisle_categories.go`
- `AisleForCategories(tags []string) *int` — as above.

### `catalog.go` (extend)
- `UpsertEAN(ctx, db Querier, rows []EanRow) (inserted, updated int, err error)` —
  `INSERT INTO ean_mappings (...) VALUES (...) ON CONFLICT (ean) DO UPDATE SET
  <all detail columns>=EXCLUDED.*, updated_at=now() RETURNING (xmax = 0)`. Same
  idempotent pattern and `Querier` interface as `UpsertFood`.

### `cmd/seed/main.go` (extend)
- New subcommand: `go run ./cmd/seed openfoodfacts [--file <path>]`, default
  `../data/food/swedish_food_products.jsonl`.
- **Streaming**: read line-by-line with a `bufio.Reader` (`ReadBytes('\n')`) — a
  plain `bufio.Scanner` is unsafe here because some JSONL lines exceed its 64 KB
  token cap. Parse each line, collect into a batch, flush via `UpsertEAN` every
  **500 rows** (bounded memory; the whole file never loads at once).
- Per-line malformed JSON or skipped (nameless) rows increment counters and are
  logged in aggregate; one bad line never aborts the run.
- Final log: `openfoodfacts: N parsed, M skipped, X inserted, Y updated`.

## Error handling

- Missing `DATABASE_URL` / unknown subcommand / missing file → log + non-zero exit
  (existing behavior).
- A malformed JSON line → counted, logged, skipped (run continues).
- A nameless row → counted as skipped (not an error).
- Idempotent: re-running upserts the same rows (insert→update), no duplicates.

## Testing (green bar `go test ./...`, docker-optional)

- **`AisleForCategories`** — table test: `["en:dairies"]`→2, `["en:seafood"]`→4,
  `["en:chocolates"]`→9, `["en:groceries"]`→6, `["en:unknown-x"]`→nil; asserts
  fish-before-pantry priority for a multi-tag product.
- **`ParseOFFLine`** — fixture lines covering: Swedish-name preference, a nameless
  skip, `nutriscore_grade:"unknown"`→nil, quantity value+unit+text split, serving
  split, `allergens_tags`/`labels_tags` "en:" stripping, a `nutriments` subset
  with some keys missing (→ null in the fixed shape), and category-derived aisle
  with name fallback.
- **`UpsertEAN`** — testcontainers: migrate up (incl. `0002`), insert N rows →
  `inserted==N`; re-run → `inserted==0, updated==N`; a changed field (e.g.
  nutriscore) updates in place with `count(*)==1`. Skips cleanly without docker.
- **Real end-to-end run** against the 434 MB file on a local Postgres: confirm
  ~23,473 imported, idempotent re-run, spot-check a known barcode's normalized
  detail and aisle.

## Out of scope (owned elsewhere)

Runtime "scan an unknown EAN → manual add → store" write endpoint (future) ·
the autocomplete query endpoint that unions `food_catalog` + `ean_mappings`
(#7) · `food_catalog` food-group enrichment (#28) · committing `data/` (stays
gitignored; the importer reads it locally by path) · JSONB GIN indexes, CHECK
constraints, and churn-free upsert (deferred).
