package catalog

import "testing"

func TestAisleFor(t *testing.T) {
	cases := []struct {
		name string
		want *int
	}{
		{"Lax, rökt", intp(4)},       // fish beats everything
		{"Mjölk 3%", intp(2)},        // dairy
		{"Äpple", intp(1)},           // produce
		{"Knäckebröd", intp(5)},      // bread
		{"Pasta, fullkorn", intp(6)}, // pantry
		{"Glass, vanilj", intp(7)},   // frozen
		{"Nötfärs", intp(3)},         // meat (nöt + färs)
		{"Kaffe, bryggt", intp(8)},   // drink
		{"Choklad, mörk", intp(9)},   // candy
		{"Xyzzy okänt", nil},         // no match
	}
	for _, c := range cases {
		got := AisleFor(c.name)
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Errorf("AisleFor(%q) = %v, want %v", c.name, deref(got), deref(c.want))
		}
	}
}

func intp(i int) *int { return &i }
func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
