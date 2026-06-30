package catalog

import (
	"strings"
	"testing"
)

const sampleKlass = `[
  {"nummer": 10, "namn": "Mjölk 3%", "klassificeringar": [
    {"typ":"LanguaL","fasett":"B Artklassificering","fasettkod":"B1","namn":"Ko"},
    {"typ":"LanguaL","fasett":"A Gruppindelning EuroFIR","fasettkod":"A1","namn":"Mjölk"}
  ]},
  {"nummer": 20, "namn": "Lax", "klassificeringar": [
    {"typ":"LanguaL","fasett":"A Gruppindelning EuroFIR","fasettkod":"A2","namn":"Fisk"}
  ]},
  {"nummer": 30, "namn": "Mystisk", "klassificeringar": [
    {"typ":"LanguaL","fasett":"B Artklassificering","fasettkod":"B9","namn":"Okänd"}
  ]}
]`

func TestParseKlassificeringar(t *testing.T) {
	m, err := ParseKlassificeringar(strings.NewReader(sampleKlass))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(m) != 2 { // nummer 30 has no EuroFIR group facet
		t.Fatalf("got %d entries, want 2: %v", len(m), m)
	}
	if m[10] != "Mjölk" || m[20] != "Fisk" {
		t.Fatalf("map = %v, want {10:Mjölk, 20:Fisk}", m)
	}
	if _, ok := m[30]; ok {
		t.Fatalf("nummer 30 should be absent (no EuroFIR facet)")
	}
}

func TestParseKlassificeringarTrimsGroup(t *testing.T) {
	const withSpace = `[{"nummer":1,"namn":"x","klassificeringar":[
		{"fasett":"A Gruppindelning EuroFIR","namn":"Konfekt och annan sockerprodukt dvs ej choklad "}]}]`
	m, err := ParseKlassificeringar(strings.NewReader(withSpace))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m[1] != "Konfekt och annan sockerprodukt dvs ej choklad" {
		t.Fatalf("group not trimmed: %q", m[1])
	}
}

func TestParseKlassificeringarMalformed(t *testing.T) {
	if _, err := ParseKlassificeringar(strings.NewReader("{not valid json")); err == nil {
		t.Fatal("expected decode error for malformed json, got nil")
	}
}

func TestFoodGroupAisle(t *testing.T) {
	cases := map[string]*int{
		"Mjölk":                        intp(2),
		"Rött kött":                    intp(3),
		"Fisk":                         intp(4),
		"Frukt och bär":                intp(1),
		"Jäst bröd":                    intp(5),
		"Pasta och liknande produkter": intp(6),
		"Glass och annan frusen dessert med mejeriprodukter": intp(7),
		"Juice och nektar":                               intp(8),
		"Choklad eller chokladprodukt":                   intp(9),
		"Konfekt och annan sockerprodukt dvs ej choklad": intp(9), // trimmed key
		"Vin och vinliknande drycker":                    nil,     // alcohol: out of scope, unmapped
		"Kosttillskott och hälsopreparat":                nil,     // supplements: unmapped
		"Totally Unknown Group":                          nil,
	}
	for group, want := range cases {
		got := FoodGroupAisle(group)
		if (got == nil) != (want == nil) || (got != nil && *got != *want) {
			t.Errorf("FoodGroupAisle(%q) = %v, want %v", group, deref(got), deref(want))
		}
	}
}
