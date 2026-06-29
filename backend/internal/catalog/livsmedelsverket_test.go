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

func TestParseLivsmedelsverketGroupFixesNameFalsePositive(t *testing.T) {
	// "Oxfilé" trips the name heuristic ("fil" -> dairy 2); the food group fixes it.
	if a := AisleFor("Oxfilé"); a == nil || *a != 2 {
		t.Fatalf("precondition: AisleFor(Oxfilé) = %v, want 2 (dairy false positive)", a)
	}
	groups := map[int]string{1: "Rött kött"}
	const src = `{"livsmedel":[{"nummer":1,"namn":"Oxfilé"}]}`
	rows, err := ParseLivsmedelsverket(strings.NewReader(src), groups)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if rows[0].Aisle == nil || *rows[0].Aisle != 3 {
		t.Fatalf("aisle = %v, want 3 (meat from food group, not dairy from name)", rows[0].Aisle)
	}
	if rows[0].FoodGroup == nil || *rows[0].FoodGroup != "Rött kött" {
		t.Fatalf("food group = %v, want Rött kött", rows[0].FoodGroup)
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
