# Seed Livsmedelsverket into food_catalog (issue #5) — design

**Status:** approved 2026-06-28
**Issue:** [#5 — Seed Livsmedelsverket generics into food_catalog](https://github.com/stroem/shopping-list/issues/5)
(milestone *M1 — Food catalog & data pipeline*)
**Builds on:** #2 (`food_catalog` table, `(source, external_id)` unique, trigram
index), #3 (`config`, `db.NewPool`).

## Goal

Make `cmd/seed` import the ~2,575 generic Swedish foods from
`data/food/livsmedelsverket_products.json` into `food_catalog` for autocomplete,
idempotently. Logic lives in a new `internal/catalog` package; `cmd/seed` stays a
thin entrypoint.

## Data reality (drives the design)

The downloaded `products.json` contains only `nummer` + `namn` per item; the food
group (`huvudgrupp`) is **not** in any downloaded file (it sits behind the
unfetched `klassificeringar` endpoint). So `aisle` is derived in this issue by a
**Swedish-keyword heuristic** over the name. Proper food-group enrichment is a
**follow-up issue**.

## v1 aisle taxonomy (canonical)

Integer → store section. This is the shared taxonomy later tickets (#7 autocomplete,
#16 ordering, store features) reference.

| aisle | section          | keywords (lowercase substring, first match wins) |
|------:|------------------|--------------------------------------------------|
| 1 | Frukt & grönt    | äpple, banan, apelsin, päron, tomat, gurka, sallad, lök, potatis, morot, paprika, broccoli, spenat, kål, svamp, champinjon, bär, jordgubb, frukt, grönsak |
| 2 | Mejeri & ägg     | mjölk, ost, yoghurt, fil, grädde, smör, ägg, kvarg, keso, mese, gräddfil |
| 3 | Kött & chark     | nöt, fläsk, kyckling, kalkon, korv, bacon, skinka, färs, lamm, biff, revben, lever, kött |
| 4 | Fisk & skaldjur  | lax, torsk, sill, makrill, tonfisk, sej, abborre, räk, krabb, mussla, hummer, fisk, skaldjur |
| 5 | Bröd & bageri    | knäckebröd, bröd, bulle, baguette, fralla, skorpa, tortilla, pita |
| 6 | Skafferi         | pasta, ris, mjöl, socker, salt, gryn, flingor, müsli, bön, lins, ärt, konserv, olja, vinäger, buljong, ketchup, senap, sås, krydd, honung, sylt |
| 7 | Fryst            | fryst, glass |
| 8 | Dryck            | juice, läsk, saft, kaffe, vatten, smoothie |
| 9 | Godis & snacks   | godis, choklad, chips, kex, snacks |

**Priority order is fish → meat → dairy → produce → bread → pantry → frozen →
drink → candy** so specific proteins (e.g. "Lax") bucket before any generic
match. No keyword match → `aisle = NULL` (honest unknown).

## Components

### `internal/catalog/aisle.go`
- `AisleFor(name string) *int` — lowercases `name`, walks the taxonomy in priority
  order, returns the first aisle whose any keyword is a substring; `nil` if none.

### `internal/catalog/catalog.go`
- `type Row struct { Source, ExternalID, Name string; Aisle *int }`.
- `type Execer interface { Exec(ctx, sql string, args ...any) (pgconn.CommandTag, error) }`
  (satisfied by `*pgxpool.Pool`).
- `UpsertFood(ctx context.Context, db Execer, rows []Row) (inserted, updated int, err error)`
  — per row `INSERT INTO food_catalog (source, external_id, name, aisle)
  VALUES (...) ON CONFLICT (source, external_id) DO UPDATE SET name=EXCLUDED.name,
  aisle=EXCLUDED.aisle, updated_at=now()`. Counts insert vs update from the
  command tag / an `xmax=0` returning trick (see plan). Idempotent.

### `internal/catalog/livsmedelsverket.go`
- `ParseLivsmedelsverket(r io.Reader) ([]Row, error)` — decodes the
  `{ "livsmedel": [ { "nummer", "namn", ... } ] }` envelope into `Row`s with
  `Source = "livsmedelsverket"`, `ExternalID = strconv(nummer)`,
  `Aisle = AisleFor(namn)`. Skips items with an empty `namn`.

### `backend/cmd/seed/main.go`
Subcommand dispatch (forward-compat with #6's `openfoodfacts`):

```
go run ./cmd/seed livsmedelsverket [--file <path>]   # default ../data/food/livsmedelsverket_products.json
go run ./cmd/seed                                     # prints usage, exit 2
```

`livsmedelsverket`: `config.Load` (needs `DATABASE_URL`) → open file →
`ParseLivsmedelsverket` → `db.NewPool` → `UpsertFood` → log `inserted/updated`.

## Error handling

- Missing `DATABASE_URL` / unknown subcommand / missing file → log + non-zero exit.
- Malformed JSON → parse error surfaced; nothing written.
- A row with empty name is skipped (not an error).

## Testing (green bar `go test ./...`, docker-optional)

- **aisle** — table test: `"Lax, rökt"`→4, `"Mjölk 3%"`→2, `"Äpple"`→1,
  `"Knäckebröd"`→5, `"Okänt xyz"`→nil. Asserts the fish-before-everything priority.
- **parse** — a 3-item fixture JSON (incl. one empty-name item that is skipped) →
  expected `Row`s with correct `ExternalID`/`Aisle`.
- **upsert** — testcontainers: migrate up, insert N rows → `inserted==N`; re-run
  same rows → `inserted==0, updated==N`; a changed name updates in place. Skips
  cleanly without docker. Verifies AC1 idempotency.

## Out of scope (owned elsewhere)

Proper `huvudgrupp`/food-group aisle enrichment (**follow-up issue**) · Open Food
Facts / EAN seeding (#6) · the autocomplete query endpoint (#7) · committing
`data/` (it stays gitignored; the importer reads it locally by path).
