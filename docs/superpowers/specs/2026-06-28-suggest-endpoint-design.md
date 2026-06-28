# Autocomplete /v1/suggest endpoint (issue #7) — design

**Status:** approved 2026-06-28
**Issue:** [#7 — Autocomplete / product search endpoint](https://github.com/stroem/shopping-list/issues/7)
(milestone *M1 — Food catalog & data pipeline*)
**Builds on:** #2 (schema: `items`, `food_catalog`, `ean_mappings`, trigram + autocomplete indexes), #3 (`config`, `db.NewPool`, router, `web` helpers), #5/#6 (seeded catalogs).

## Goal

Add `GET /v1/suggest?q=` — the endpoint that powers add-item autocomplete. It
ranks the household's own previously-bought `items` first (by purchase
frequency), then the generic Livsmedelsverket `food_catalog`, then branded Open
Food Facts `ean_mappings`. Every result carries an `aisle` so the app can sort
the list by store section.

## Identity decoupling (key scope decision)

#7 must scope suggestions to a household, but the identity/sharing foundation —
**now intended to be Google sign-in + a shareable household invite link** — is
**issue #8 (Households & device identity)**, scheduled in M2. To avoid pulling
auth into a catalog ticket, #7 is **decoupled from the auth mechanism** behind a
one-function household-resolution **seam**:

- Today the seam resolves provisionally: `X-Device-Id` header → `users.device_id`
  → `household_id`. An unknown/missing device resolves to **no household**, which
  yields **catalog-only** results (correct and testable).
- When #8 lands Google-auth + invite links, it swaps the seam's body (or sets the
  household in request context); the ranking query below is unchanged and the
  household branch "lights up" automatically. **Nothing in #7 is throwaway.**

The Google-auth + invite-link direction (a pivot away from AGENTS.md's current
"no OAuth v1 / shared-secret UUID" identity model) is recorded on **#8** and the
AGENTS.md change is owned there, not here.

## Endpoint contract

```
GET /v1/suggest?q=<text>&limit=<n>
Header (optional): X-Device-Id: <id>

200 OK  → [ { "name": "Mjölk 3%", "aisle": 2, "source": "food_catalog",
              "image_url": null, "ean": null }, … ]
```

- `q`: the typed text. Empty/whitespace → `200 []` (debounce-friendly; a cleared
  input is not an error).
- `limit`: optional, default **10**, capped at **25**; unparseable → default.
- `source`: one of `items` | `food_catalog` | `openfoodfacts` (provenance, for UI
  hints).
- `aisle`: nullable int (the v1 1–9 taxonomy); present so the app can sort.
- `image_url`: nullable; `ean`: nullable, populated **only** for `openfoodfacts`
  rows so the app can link a suggestion to a scannable product.

## Components

### `internal/suggest/suggest.go`
- `type Suggestion struct { Name string; Aisle *int; Source string; ImageURL *string; EAN *string }`
- `type Querier interface { Query(ctx, sql string, args ...any) (pgx.Rows, error) }`
  (satisfied by `*pgxpool.Pool`).
- `type Service struct { db Querier }` + `func New(db Querier) *Service`.
- `func (s *Service) Suggest(ctx, deviceID, q string, limit int) ([]Suggestion, error)`:
  trims `q` (empty → `nil, nil`), clamps `limit` to `[1,25]`, resolves the
  household via the seam, runs the ranked query, scans results.
- `resolveHousehold(ctx, db, deviceID string) (*string, error)` — the seam:
  `deviceID==""` → `nil`; else `SELECT household_id FROM users WHERE device_id=$1
  AND deleted_at IS NULL`; no row / NULL → `nil`. Returned as `*string` so a nil
  household passes as SQL `NULL`.

### `internal/router/suggest.go`
- `func suggestHandler(s *suggest.Service) http.HandlerFunc` — reads `q` and
  `limit` from the query string and `X-Device-Id` via `web.DeviceID(ctx)`, calls
  `s.Suggest`, responds with `web.JSON(w, 200, results)`. On service error →
  `web.Error(w, 500, …)`. Always returns a JSON array (never `null`: start from
  `[]Suggestion{}`).

### Router wiring (`internal/router/router.go`)
- `Deps` gains `Suggest *suggest.Service`.
- New `/v1` route group: `r.Route("/v1", func(r){ r.Get("/suggest", suggestHandler(deps.Suggest)) })`. `/healthz` stays at root.
- `cmd/api` and `cmd/lambda` construct `suggest.New(pool)` and pass it in `Deps`
  (same handler both places).

## Ranking query (single round trip)

`UNION ALL` over the three sources, each tagged with a `src_rank` (items 0,
food_catalog 1, ean_mappings 2). A row matches when
`name ILIKE q || '%' OR name % q` (prefix **or** trigram similarity — both
index-backed by the GIN trigram indexes and `items_autocomplete`). The household
param is a `*string` (`nil` ⇒ the items branch matches nothing ⇒ catalog-only).

```sql
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
LIMIT $3;
```

- **Dedup:** `DISTINCT ON (lower(name))` collapses the same product appearing in
  several sources, keeping the best (lowest `src_rank`, then frequency) — so a
  household item named "Mjölk" suppresses the catalog "Mjölk".
- **Order:** items before generics before branded; within items, purchase
  frequency; within catalog, prefix beats fuzzy, then similarity, then shorter
  names.
- `$2::uuid` is `NULL` when no household resolved → `household_id = NULL` excludes
  all items (catalog-only). `$1` = trimmed `q`, `$3` = clamped `limit`.

## Error handling

- Missing/empty/whitespace `q` → `200 []` (no DB hit).
- Bad/absent `limit` → default 10; values >25 clamp to 25, <1 clamp to 10.
- DB/query error → JSON 500 via `web.Error`; panics caught by `web.Recoverer`.
- Cross-household: items are only ever filtered by the resolved household id; an
  unknown device returns zero items. No existence leak.

## Testing (green bar `go test ./...`, docker-optional)

- **Handler (no DB)** — a fake `Suggester`: empty `q` → `[]`; `limit` parse +
  cap (e.g. `limit=999` → 25, `limit=abc` → 10); response is a JSON array with
  the documented fields; missing `X-Device-Id` still 200.
- **Service via testcontainers** (skip without docker) — migrate up, insert two
  households' `items`, plus `food_catalog` and `ean_mappings` rows, then assert:
  1. a household item ranks **before** a catalog row for the same query;
  2. prefix match ranks before a fuzzy-only match;
  3. duplicate names across sources appear **once** (dedup), household-sourced;
  4. household **isolation** — household A's query never returns household B's
     items; an unknown device returns catalog-only;
  5. every result carries `source`; `ean` is set only for `openfoodfacts` rows;
  6. `limit` is honored.

## Out of scope (owned elsewhere)

Google sign-in + household invite links and the AGENTS.md identity pivot (#8) ·
the autocomplete **UI** (frequency-ranked, debounced) (#15) · writing/maintaining
`items` (purchase history that feeds ranking comes from #11) · realtime/push ·
per-store aisle ordering (#22). #7 only reads existing tables and ships the
ranked query behind the household seam.
