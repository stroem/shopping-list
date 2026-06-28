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
	in.WriteString("{not valid json\n")            // malformed
	in.WriteString(`{"brands":"acme"}` + "\n")     // nameless + codeless -> skipped
	in.WriteString(offLine(t, "333", "Valid Two")) // proves a bad line didn't abort

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

func TestStreamOFFTrailingLineNoNewline(t *testing.T) {
	var in strings.Builder
	in.WriteString(offLine(t, "AAA", "First"))
	in.WriteString(offLine(t, "BBB", "Second"))
	in.WriteString(offLine(t, "CCC", "Third"))

	// Strip the final newline so the last line has no terminating \n.
	payload := strings.TrimSuffix(in.String(), "\n")

	var batches [][]catalog.EanRow
	stats, err := streamOFF(strings.NewReader(payload), 500, recorder(&batches))
	if err != nil {
		t.Fatalf("streamOFF: %v", err)
	}
	if stats.Parsed != 3 {
		t.Fatalf("stats = %+v, want Parsed 3", stats)
	}
	if stats.Inserted != 3 {
		t.Fatalf("stats = %+v, want Inserted 3", stats)
	}

	// The last row (CCC) must have been flushed via the post-EOF final flush.
	got := map[string]bool{}
	for _, b := range batches {
		for _, row := range b {
			got[row.EAN] = true
		}
	}
	for _, ean := range []string{"AAA", "BBB", "CCC"} {
		if !got[ean] {
			t.Fatalf("EAN %s never reached upsert; got %v", ean, got)
		}
	}
}

func TestStreamOFFLongLineNotTruncated(t *testing.T) {
	longName := strings.Repeat("a", 70_000)

	var in strings.Builder
	in.WriteString(offLine(t, "LONG001", longName))

	var batches [][]catalog.EanRow
	stats, err := streamOFF(strings.NewReader(in.String()), 500, recorder(&batches))
	if err != nil {
		t.Fatalf("streamOFF: %v", err)
	}
	if stats.Parsed != 1 {
		t.Fatalf("stats = %+v, want Parsed 1", stats)
	}

	// Find the recorded row for EAN "LONG001" and verify name length is preserved.
	var found *catalog.EanRow
	for i := range batches {
		for j := range batches[i] {
			if batches[i][j].EAN == "LONG001" {
				found = &batches[i][j]
				break
			}
		}
		if found != nil {
			break
		}
	}
	if found == nil {
		t.Fatal("EAN LONG001 never reached upsert")
	}
	if len(found.Name) != 70_000 {
		t.Fatalf("Name length = %d, want 70000 (name was truncated)", len(found.Name))
	}
}
