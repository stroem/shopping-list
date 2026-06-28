# Unit-test the `cmd/seed` streaming loop — design

**Issue:** [#30](https://github.com/stroem/shopping-list/issues/30) — Unit-test the cmd/seed streaming loop (long lines, malformed, nameless)
**Milestone:** M1 — Food catalog & data pipeline
**Date:** 2026-06-28
**Status:** approved

## Problem

`cmd/seed openfoodfacts` (#6, #29) streams a 434 MB JSONL through a `bufio.Reader`,
batches 500 rows per upsert, and tolerates malformed / nameless / codeless lines
(it counts them and continues). That streaming behavior — including the deliberate
use of `bufio.Reader` over `bufio.Scanner` to survive lines above Scanner's 64 KB
cap, and the "one bad line never aborts the run" tolerance — is currently proven
**only** by a manual end-to-end run. There is no automated test, so the streaming
acceptance criteria are not demonstrated in CI.

## Goal

Convert the streaming path from manual-only to demonstrated, with **no behavior
change** to production seeding.

## Approach

Extract the inline read/batch/flush loop in `seedOpenFoodFacts` into a package-level
function behind an **upsert callback**, then unit-test that function with in-memory
JSONL — no database required. The callback seam is what lets the test exercise the
real batching/flush logic while substituting an in-memory recorder for the DB upsert.

Rejected alternatives:
- **Test against a real testcontainers Postgres** — defeats the issue's "without a
  DB" requirement and is slow; the parse/batch/flush logic does not need a DB.
- **Extract only the per-line parse, leave batching inline** — leaves the
  batch/flush boundary (the part that actually broke under Scanner's 64 KB cap)
  untested.

## Component

In `backend/cmd/seed/main.go`:

```go
// seedStats holds the running counts produced while streaming OFF JSONL.
type seedStats struct {
	Parsed    int
	Skipped   int
	Malformed int
	Inserted  int
	Updated   int
}

// streamOFF reads Open Food Facts JSONL from r, batches parsed rows up to
// batchSize, and flushes each full batch through upsert (plus a final flush at
// EOF). A malformed line (ParseOFFLine error) or a skippable line (nameless /
// codeless) is counted and the stream continues — one bad line never aborts.
// It returns the accumulated stats and the first read or upsert error.
func streamOFF(r io.Reader, batchSize int, upsert func(batch []catalog.EanRow) (ins, upd int, err error)) (seedStats, error)
```

Behavior of `streamOFF`, preserved verbatim from the current inline loop:

- Read with `bufio.NewReaderSize(r, 1<<20)` and `ReadBytes('\n')` (the >64 KB-safe
  path). A non-empty trailing line without a final newline is still processed.
- For each non-empty line, call `catalog.ParseOFFLine(line)`:
  - error → `Malformed++`
  - `ok == false` → `Skipped++`
  - else → `Parsed++`, append the row to the batch; when the batch reaches
    `batchSize`, flush it.
- `flush` calls `upsert(batch)`, adds the returned `ins`/`upd` to
  `Inserted`/`Updated`, resets the batch, and (in production) logs progress.
- After `io.EOF`, do a final flush of any remaining rows.
- A read error other than `io.EOF`, or an `upsert` error, is returned immediately.

`seedOpenFoodFacts` keeps its existing responsibilities — flag parsing, `config.Load`,
opening the file, and creating the `pgxpool` — and calls `streamOFF`, passing a
closure that invokes `catalog.UpsertEAN(ctx, pool, batch)`. The per-flush progress
log (`openfoodfacts: progress — N parsed, N inserted, N updated`) lives inside
`streamOFF`'s `flush` so the exact line is preserved without leaking running totals
back to the caller; the end-of-run summary log stays in `seedOpenFoodFacts` and is
unchanged. This keeps the production path and its log output identical to today.

**One documented deviation (diagnostics only, not control flow or log output):** the
original wrapped a non-EOF *read* error as `read <file>: <err>` but returned an
*upsert* error raw. After extraction, `streamOFF` no longer knows the filename, so
`seedOpenFoodFacts` wraps **any** error from `streamOFF` as `read <file>: <err>`.
The only effect is that a (rare) upsert error now carries the same `read <file>:`
prefix — error-message text only; happy-path behavior and all logging are identical.

## Testing (the deliverable)

`backend/cmd/seed/main_test.go`, package `main`, no DB.

**Test 1 — mixed-line tolerance and counts.** Build one in-memory JSONL fixture
(via `strings.Builder`/`bytes.Buffer`) containing, in order:

1. a **valid** product (name + code) → `Parsed`;
2. a line **> 64 KB**: valid JSON whose `product_name` is padded past 64 KB, proving
   `bufio.Reader` reads beyond Scanner's 64 KB cap and that the line parses
   (→ `Parsed`);
3. a **malformed** line (not valid JSON, e.g. `{not json`) → `Malformed`;
4. a **nameless** line (valid JSON with neither `product_name` nor `code`) → `Skipped`;
5. a second **valid** product after the bad lines → `Parsed` (proves a bad line did
   not abort the run).

Pass an in-memory `upsert` that appends each batch to a recorder and returns
`(len(batch), 0, nil)`. Assert:
- `stats.Parsed == 3`, `stats.Malformed == 1`, `stats.Skipped == 1`,
  `stats.Inserted == 3`, and `err == nil`;
- every valid row (including the >64 KB one and the post-error row) reached the
  callback — i.e. the flattened recorded batches contain all three EANs.

**Test 2 — batching/flush boundary.** Feed several valid rows with `batchSize == 2`
and assert the recorder saw more than one batch (the loop flushes mid-stream, not
only at EOF), and that the total rows upserted equals the number of valid rows.

A tiny JSON fixture helper builds OFF lines from a code + name so the tests stay
readable; the >64 KB line reuses it with a padded name.

## Constraints

- **No behavior change** to the production seeding path or its log output.
- Green bar = `go test ./...` from `backend/`. The new `cmd/seed` tests need **no
  database** and must pass in CI without Docker.
- No new third-party dependency (standard library + existing `internal/catalog`).
- Conventional Commits 1.0.0; **no AI/Claude attribution**. Commit only on the
  branch `chore/issue-30-seed-stream-test`; never `main`.

## Files touched

- `backend/cmd/seed/main.go` — extract `streamOFF` + `seedStats`; `seedOpenFoodFacts`
  calls it via a DB-backed closure (no behavior change).
- `backend/cmd/seed/main_test.go` — new; the two tests above + fixture helper.
