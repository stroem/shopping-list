package catalog

import "testing"

func TestAisleForCategories(t *testing.T) {
	cases := []struct {
		name string
		tags []string
		want *int
	}{
		{"dairy", []string{"en:dairies", "en:cheeses"}, intp(2)},
		{"seafood beats dairy", []string{"en:dairies", "en:seafood"}, intp(4)},
		{"meat", []string{"en:meats", "en:hams"}, intp(3)},
		{"produce", []string{"en:fresh-vegetables"}, intp(1)},
		{"bread", []string{"en:breads"}, intp(5)},
		{"frozen", []string{"en:frozen-desserts"}, intp(7)},
		{"drink incl alcohol", []string{"en:alcoholic-beverages"}, intp(8)},
		{"candy", []string{"en:chocolates"}, intp(9)},
		{"pantry catch-all", []string{"en:groceries", "en:sauces"}, intp(6)},
		{"no match", []string{"en:made-in-sweden"}, nil},
		{"empty", nil, nil},
		{"ice-cream is frozen not dairy", []string{"en:ice-creams"}, intp(7)},
		{"eggplant is produce not dairy", []string{"en:eggplants"}, intp(1)},
		{"real cream stays dairy", []string{"en:creams"}, intp(2)},
		{"oil no longer matches boiled", []string{"en:boiled-vegetables"}, intp(1)},
		{"compound beats competing dairy tag", []string{"en:dairies", "en:ice-creams"}, intp(7)},
		{"potatoes are produce", []string{"en:potatoes"}, intp(1)},
		{"sweet potato is produce not candy", []string{"en:sweet-potatoes"}, intp(1)},
		{"fishes are seafood", []string{"en:fishes"}, intp(4)},
	}
	for _, c := range cases {
		got := AisleForCategories(c.tags)
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Errorf("%s: AisleForCategories(%v) = %v, want %v", c.name, c.tags, deref(got), deref(c.want))
		}
	}
}
