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
