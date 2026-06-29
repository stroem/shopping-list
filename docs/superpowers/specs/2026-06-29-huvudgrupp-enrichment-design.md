# Enrich `food_catalog` with Livsmedelsverket food group — design

**Issue:** [#28](https://github.com/stroem/shopping-list/issues/28) — Enrich food_catalog with Livsmedelsverket food group (huvudgrupp) for aisle mapping
**Milestone:** M1 — Food catalog & data pipeline
**Date:** 2026-06-29
**Status:** approved

## Problem

[#5](https://github.com/stroem/shopping-list/issues/5) seeded ~2,575 Livsmedelsverket
generics into `food_catalog`, but the downloaded `products.json` only has `nummer` +
`namn`. The authoritative food group sits behind the Livsmedelsverket API's
`klassificeringar` endpoint, which was never fetched, so `food_catalog.food_group` is
NULL and `aisle` is derived from a Swedish-keyword heuristic (`AisleFor`,
`internal/catalog/aisle.go`) that classifies only ~79% of rows and has known
false positives (`gris`→`ris`, `filé`→`fil`).

## Goal

Populate `food_catalog.food_group` from the authoritative Livsmedelsverket
classification and use it to drive `aisle`, preferring the food group over the name
keyword and falling back to the keyword heuristic only when no food group exists.
Idempotent and re-runnable.

## Data acquisition (one-off data-prep)

The `klassificeringar` for all 2,575 products is fetched once (rate-limited, resumable)
into **`data/food/livsmedelsverket_klassificeringar.json`** (gitignored, like all of
`data/`), shaped:

```json
[{"nummer": 1, "namn": "Nöt talg", "klassificeringar": [ {"typ": "...", "fasett": "...", "fasettkod": "...", "namn": "...", "langualId": "..."}, ... ]}, ...]
```

`cmd/seed` **ingests this downloaded copy**; the Go module contains no network code.
The fetch is data-prep external to the build (mirroring the existing
`data/fetch_livsmedel_details.py` pattern).

## Food group source

Each product's `klassificeringar` is a list of LanguaL/EuroFIR facets. The **food
group** is the facet whose `fasett == "A Gruppindelning EuroFIR"`; its `namn` (e.g.
`"Mjölk och mjölkprodukter"`, `"Kött och köttprodukter"`) is stored verbatim in
`food_catalog.food_group`. A product with no such facet has no food group (NULL).

## EuroFIR group → aisle mapping

A static `map[string]int` from EuroFIR group name to the v1 aisle taxonomy, in a new
`internal/catalog/food_group.go`. The table covers the **distinct EuroFIR group names
present in the fetched data** (enumerated when writing the implementation plan, so the
table is data-driven and complete, not guessed). Groups that have no sensible aisle
(e.g. very generic or non-food-ish EuroFIR groups) are simply omitted from the table —
they yield `food_group` set but `aisle` via fallback.

```go
// FoodGroupAisle returns the aisle for a Livsmedelsverket/EuroFIR food group,
// or nil when the group is unknown/unmapped.
func FoodGroupAisle(group string) *int
```

## Precedence

For each product:

- `food_group` = the EuroFIR group name if present, else NULL.
- `aisle`:
  1. `FoodGroupAisle(group)` when a group exists **and** maps to an aisle;
  2. else `AisleFor(name)` — the existing Swedish-keyword heuristic;
  3. else nil.

Leaning on the food group both raises coverage and removes the keyword false
positives for any product that has a group.

## Code changes (`internal/catalog`)

- **`Row`** gains `FoodGroup *string`.
- **`upsertFoodSQL`** writes `food_group`:
  `INSERT … (source, external_id, name, food_group, aisle) VALUES ($1,$2,$3,$4,$5)`
  with `ON CONFLICT (source, external_id) DO UPDATE SET name=EXCLUDED.name,
  food_group=EXCLUDED.food_group, aisle=EXCLUDED.aisle, updated_at=now()`. Same
  conflict key → idempotent and re-runnable. `UpsertFood` passes `r.FoodGroup`.
- **`ParseKlassificeringar(r io.Reader) (map[int]string, error)`** decodes the
  klassificeringar file into `nummer → EuroFIR group name` (only products that have
  the `"A Gruppindelning EuroFIR"` facet appear in the map).
- **`ParseLivsmedelsverket`** is extended to accept the group map so each `Row`'s
  `FoodGroup` and group-preferred `Aisle` are set. Signature:
  `ParseLivsmedelsverket(r io.Reader, groups map[int]string) ([]Row, error)`. When
  `groups` is nil/empty, behavior is exactly today's (food group NULL, aisle from
  name) — preserving the no-klass path.
- **`food_group.go`** — the `FoodGroupAisle` table + helper.

## `cmd/seed livsmedelsverket`

Add a `--klass` flag (default `../data/food/livsmedelsverket_klassificeringar.json`).
On run: open `--file` (products) as today; if the `--klass` file exists, parse it via
`ParseKlassificeringar` and pass the map into `ParseLivsmedelsverket`; if it does not
exist, log a notice and proceed name-only (so the command still runs without the new
file). Log a summary line including how many rows got a food group.

## Out of scope

- OFF `AisleForCategories` (shipped in #31).
- `AisleFor`'s own `gris`/`ris`, `filé`/`fil` false positives — not modified here,
  though food-group precedence reduces reliance on it.
- No schema migration: `food_catalog.food_group` already exists (`0001_init`).

## Testing

`internal/catalog`:

- **`ParseKlassificeringar`** — extracts the `"A Gruppindelning EuroFIR"` facet `namn`;
  ignores other facets (`B Artklassificering`, etc.); a product lacking that facet is
  absent from the map; malformed entry handling is decode-error, not panic.
- **`FoodGroupAisle`** — known groups map to expected aisles (e.g. a dairy group → 2,
  a meat group → 3, a fish group → 4); unknown group → nil. (Exact group strings taken
  from the fetched data.)
- **`ParseLivsmedelsverket` with groups** — a product with a mapped group gets that
  aisle **and** `FoodGroup` set; a product whose group is unmapped gets `FoodGroup` set
  and aisle from the name fallback; a product absent from the map keeps today's
  name-only behavior; empty `groups` map = unchanged behavior.
- **`UpsertFood` round-trip** (DB-backed, skips without Docker) — `food_group` and
  `aisle` persist; a second run is idempotent (updates, not duplicates) and updates a
  changed `food_group`.

Green bar = `go test ./...` (DB tests skip cleanly without Docker). No new third-party
dependency.

## Files touched

- `backend/internal/catalog/food_group.go` — new; `FoodGroupAisle` + table.
- `backend/internal/catalog/food_group_test.go` — new; table + `ParseKlassificeringar` tests.
- `backend/internal/catalog/livsmedelsverket.go` — `ParseKlassificeringar`; enriched `ParseLivsmedelsverket`.
- `backend/internal/catalog/livsmedelsverket_test.go` — enriched-parse tests.
- `backend/internal/catalog/catalog.go` — `Row.FoodGroup`; `upsertFoodSQL`; `UpsertFood`.
- `backend/internal/catalog/upsert_test.go` — `food_group` round-trip/idempotency.
- `backend/cmd/seed/main.go` — `--klass` flag + wiring.
- `AGENTS.md` — note that `cmd/seed livsmedelsverket` reads the klassificeringar file (proposed).
