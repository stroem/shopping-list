# Seed streaming-loop unit tests Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `cmd/seed`'s OFF streaming loop unit-testable without a DB by extracting it into a `streamOFF` seam, and cover long(>64 KB)/malformed/nameless/valid lines + batch flushing.

**Architecture:** Extract the inline read/batch/flush loop in `seedOpenFoodFacts` into a package-level `streamOFF(io.Reader, batchSize, upsert) (seedStats, error)`. Production wires a DB-backed `upsert` closure; tests wire an in-memory recorder. TDD: the tests reference `streamOFF` before it exists (compile-red), then the extraction makes them green.

**Tech Stack:** Go 1.26, standard library (`bufio`, `io`, `testing`), existing `internal/catalog`.

## Global Constraints

- **No behavior change** to the production seeding path or its log output. The per-flush progress log line `openfoodfacts: progress — %d parsed, %d inserted, %d updated` and the end-of-run summary are preserved.
- One documented deviation (diagnostics only): `seedOpenFoodFacts` wraps any `streamOFF` error as `read <file>: %w`, so a rare upsert error now carries that prefix. Error text only — not control flow or logging.
- Green bar = `go test ./...` from `backend/`. The new `cmd/seed` tests need **no database** and must pass without Docker.
- No new third-party dependency (stdlib + existing `internal/catalog`).
- Conventional Commits 1.0.0; **no AI/Claude attribution**. Commit only on branch `chore/issue-30-seed-stream-test`; never `main`.

---

### Task 1: Extract `streamOFF` and unit-test the streaming loop

**Files:**
- Modify: `backend/cmd/seed/main.go` (extract `seedStats` + `streamOFF`; rewrite `seedOpenFoodFacts`'s loop to call it)
- Test: `backend/cmd/seed/main_test.go` (create)

**Interfaces:**
- Consumes: `catalog.ParseOFFLine(line []byte) (catalog.EanRow, bool, error)`, `catalog.EanRow` (fields incl. `EAN string`), `catalog.UpsertEAN(ctx, pool, []catalog.EanRow) (int, int, error)`.
- Produces:
  - `type seedStats struct { Parsed, Skipped, Malformed, Inserted, Updated int }`
  - `func streamOFF(r io.Reader, batchSize int, upsert func(batch []catalog.EanRow) (ins, upd int, err error)) (seedStats, error)`

- [ ] **Step 1: Write the failing tests**

Create `backend/cmd/seed/main_test.go`:

```go
package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stroem/shopping-list/backend/internal/catalog"
)

// offLine builds a minimal valid Open Food Facts JSONL line from a code + name.
func offLine(t *testing.T, code, name string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"code": code, "product_name": name})
	if err != nil {
		t.Fatalf("marshal off line: %v", err)
	}
	return string(b) + "\n"
}

// recorder is an in-memory upsert: it records each batch and reports every row
// as an insert, so streamOFF's Inserted total equals the rows it flushed.
func recorder(batches *[][]catalog.EanRow) func([]catalog.EanRow) (int, int, error) {
	return func(batch []catalog.EanRow) (int, int, error) {
		cp := make([]catalog.EanRow, len(batch))
		copy(cp, batch)
		*batches = append(*batches, cp)
		return len(batch), 0, nil
	}
}

func TestStreamOFFMixedLines(t *testing.T) {
	var in strings.Builder
	in.WriteString(offLine(t, "111", "Valid One"))
	// A line well over bufio.Scanner's 64 KB cap: valid JSON with a padded name.
	in.WriteString(offLine(t, "222", strings.Repeat("a", 70_000)))
	in.WriteString("{not valid json\n")                 // malformed
	in.WriteString(`{"brands":"acme"}` + "\n")          // nameless + codeless -> skipped
	in.WriteString(offLine(t, "333", "Valid Two"))      // proves a bad line didn't abort

	var batches [][]catalog.EanRow
	stats, err := streamOFF(strings.NewReader(in.String()), 500, recorder(&batches))
	if err != nil {
		t.Fatalf("streamOFF: %v", err)
	}
	if stats.Parsed != 3 || stats.Malformed != 1 || stats.Skipped != 1 || stats.Inserted != 3 {
		t.Fatalf("stats = %+v, want Parsed 3, Malformed 1, Skipped 1, Inserted 3", stats)
	}

	// All three valid EANs must have reached the upsert callback.
	got := map[string]bool{}
	for _, b := range batches {
		for _, row := range b {
			got[row.EAN] = true
		}
	}
	for _, ean := range []string{"111", "222", "333"} {
		if !got[ean] {
			t.Fatalf("EAN %s never reached upsert; got %v", ean, got)
		}
	}
}

func TestStreamOFFFlushesMidStream(t *testing.T) {
	var in strings.Builder
	for _, c := range []string{"1", "2", "3", "4", "5"} {
		in.WriteString(offLine(t, c, "Name "+c))
	}

	var batches [][]catalog.EanRow
	stats, err := streamOFF(strings.NewReader(in.String()), 2, recorder(&batches))
	if err != nil {
		t.Fatalf("streamOFF: %v", err)
	}
	if stats.Parsed != 5 || stats.Inserted != 5 {
		t.Fatalf("stats = %+v, want Parsed 5, Inserted 5", stats)
	}
	if len(batches) < 2 {
		t.Fatalf("expected multiple flushes with batchSize=2, got %d batch(es)", len(batches))
	}
	total := 0
	for _, b := range batches {
		total += len(b)
	}
	if total != 5 {
		t.Fatalf("upserted %d rows across batches, want 5", total)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/seed/ -run TestStreamOFF -v`
Expected: compile failure — `streamOFF` and `seedStats` undefined.

- [ ] **Step 3: Extract `seedStats` + `streamOFF` and rewire the caller**

In `backend/cmd/seed/main.go`, add the type + function (place them above `seedOpenFoodFacts`):

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
// batchSize, and flushes each full batch (and the remainder at EOF) through
// upsert. A malformed line (ParseOFFLine error) or a skippable line (nameless/
// codeless) is counted and the stream continues — one bad line never aborts.
// It uses bufio.Reader (not Scanner) so lines above Scanner's 64 KB cap stream
// fine. Returns the accumulated stats and the first read or upsert error.
func streamOFF(r io.Reader, batchSize int, upsert func(batch []catalog.EanRow) (ins, upd int, err error)) (seedStats, error) {
	var (
		stats seedStats
		batch []catalog.EanRow
	)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		ins, upd, err := upsert(batch)
		if err != nil {
			return err
		}
		stats.Inserted += ins
		stats.Updated += upd
		batch = batch[:0]
		log.Printf("openfoodfacts: progress — %d parsed, %d inserted, %d updated", stats.Parsed, stats.Inserted, stats.Updated)
		return nil
	}

	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			row, ok, perr := catalog.ParseOFFLine(line)
			switch {
			case perr != nil:
				stats.Malformed++
			case !ok:
				stats.Skipped++
			default:
				stats.Parsed++
				batch = append(batch, row)
				if len(batch) >= batchSize {
					if ferr := flush(); ferr != nil {
						return stats, ferr
					}
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return stats, err
		}
	}
	if ferr := flush(); ferr != nil {
		return stats, ferr
	}
	return stats, nil
}
```

Then replace the body of `seedOpenFoodFacts` from the `// bufio.Reader (not Scanner)...`
comment through the final summary `log.Printf` with a call to `streamOFF`. The new
tail of `seedOpenFoodFacts` (after `defer pool.Close()`) reads:

```go
	stats, err := streamOFF(f, *batchSize, func(batch []catalog.EanRow) (int, int, error) {
		return catalog.UpsertEAN(ctx, pool, batch)
	})
	if err != nil {
		return fmt.Errorf("read %s: %w", *file, err)
	}

	log.Printf("openfoodfacts: done — %d parsed, %d skipped, %d malformed, %d inserted, %d updated",
		stats.Parsed, stats.Skipped, stats.Malformed, stats.Inserted, stats.Updated)
	return nil
}
```

Leave all imports as-is — `bufio`, `io`, and `log` are still used (now inside
`streamOFF`); `context`, `fmt`, `flag`, `os`, and the three internal packages remain
used by `seedOpenFoodFacts`. Confirm `goimports`/`go build` is happy (no unused imports).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/seed/ -v`
Expected: PASS — `TestStreamOFFMixedLines`, `TestStreamOFFFlushesMidStream`.

- [ ] **Step 5: Build, vet, and full suite**

Run: `go build ./... && go vet ./... && gofmt -l cmd/seed/`
Expected: no output (build+vet clean, gofmt reports no files).

Run: `go test ./...`
Expected: all packages ok; DB-backed packages skip cleanly without Docker. `cmd/seed` now reports `ok` (was `[no test files]`).

- [ ] **Step 6: Commit**

```bash
git add cmd/seed/main.go cmd/seed/main_test.go
git commit -m "test(seed): unit-test OFF streaming loop via streamOFF seam

Extract seedOpenFoodFacts' read/batch/flush loop into a package-level
streamOFF(io.Reader, batchSize, upsert) returning seedStats, so the
streaming path is demonstrated without a DB: a >64KB line, a malformed
line, a nameless line, and mid-stream batch flushing. No behavior change
to production seeding.

Closes #30"
```

---

## Self-Review

**Spec coverage:**
- Extract read/batch/flush into a function taking `io.Reader` + upsert callback → Task 1 Step 3 (`streamOFF`). ✓
- Unit test with >64 KB line, malformed, nameless, valid; assert counts; one bad line never aborts → `TestStreamOFFMixedLines`. ✓
- Batch flush boundary → `TestStreamOFFFlushesMidStream` (`batchSize=2`, asserts ≥2 batches). ✓
- No behavior change; `go test ./...` green → progress + summary logs preserved (Step 3); Step 5 runs the full suite. ✓
- No DB required → tests use `strings.NewReader` + in-memory `recorder`. ✓

**Placeholder scan:** none — all steps carry real code/commands.

**Type consistency:** `seedStats` fields and `streamOFF` signature in the Interfaces block match Step 3's code; the test's `recorder`/`offLine` helpers and `catalog.EanRow.EAN` usage match the `internal/catalog` signatures. ✓
