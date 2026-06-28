// Package catalog imports food reference data (Livsmedelsverket generics, and
// later Open Food Facts) into Postgres for autocomplete.
package catalog

import "strings"

// aisleGroup is one store section and the lowercase name-substrings that map to it.
type aisleGroup struct {
	aisle    int
	keywords []string
}

// aisleGroups is the v1 aisle taxonomy, in match-priority order: specific
// proteins (fish, meat) before generic matches. This integer space is the shared
// aisle taxonomy other features reference. Unmatched names get no aisle.
var aisleGroups = []aisleGroup{
	{4, []string{"lax", "torsk", "sill", "makrill", "tonfisk", "sej", "abborre", "räk", "krabb", "mussla", "hummer", "fisk", "skaldjur"}},
	{3, []string{"nöt", "fläsk", "kyckling", "kalkon", "korv", "bacon", "skinka", "färs", "lamm", "biff", "revben", "lever", "kött"}},
	{2, []string{"mjölk", "ost", "yoghurt", "fil", "grädde", "smör", "ägg", "kvarg", "keso", "mese", "gräddfil"}},
	{1, []string{"äpple", "banan", "apelsin", "päron", "tomat", "gurka", "sallad", "lök", "potatis", "morot", "paprika", "broccoli", "spenat", "kål", "svamp", "champinjon", "bär", "jordgubb", "frukt", "grönsak"}},
	{5, []string{"knäckebröd", "bröd", "bulle", "baguette", "fralla", "skorpa", "tortilla", "pita"}},
	{6, []string{"pasta", "ris", "mjöl", "socker", "salt", "gryn", "flingor", "müsli", "bön", "lins", "ärt", "konserv", "olja", "vinäger", "buljong", "ketchup", "senap", "sås", "krydd", "honung", "sylt"}},
	{7, []string{"fryst", "glass"}},
	{8, []string{"juice", "läsk", "saft", "kaffe", "vatten", "smoothie"}},
	{9, []string{"godis", "choklad", "chips", "kex", "snacks"}},
}

// AisleFor returns the first aisle whose keyword is a substring of the lowercased
// name, by taxonomy priority, or nil when nothing matches.
func AisleFor(name string) *int {
	n := strings.ToLower(name)
	for _, g := range aisleGroups {
		for _, kw := range g.keywords {
			if strings.Contains(n, kw) {
				a := g.aisle
				return &a
			}
		}
	}
	return nil
}
