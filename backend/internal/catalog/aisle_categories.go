package catalog

import "strings"

// categoryGroups maps OFF English category-tag keywords to the v1 aisle
// taxonomy, in priority order (specific proteins first, pantry last as the
// catch-all). Keywords are matched against whole tag segments by
// AisleForCategories, not as raw substrings. Multi-word keywords (e.g.
// "ice-cream") are more specific and win over single-word ones.
var categoryGroups = []aisleGroup{
	{4, []string{"seafood", "fish", "salmon", "tuna", "shellfish", "shrimp", "herring", "mackerel"}},
	{3, []string{"meat", "poultry", "beef", "pork", "chicken", "ham", "sausage", "bacon", "charcuterie", "turkey"}},
	{2, []string{"dairies", "dairy", "milk", "cheese", "yogurt", "yoghurt", "butter", "cream", "egg"}},
	{1, []string{"fruit", "vegetable", "legume", "salad", "potato", "berries", "mushroom", "eggplant"}},
	{5, []string{"bread", "bakery", "viennoiserie", "baguette", "toast", "crackers"}},
	{7, []string{"frozen", "ice-cream"}},
	{8, []string{"beverage", "water", "juice", "soda", "coffee", "tea", "drink", "wine", "beer", "alcoholic", "spirit"}},
	{9, []string{"chocolate", "candy", "candies", "snack", "biscuit", "chips", "confectioner", "sweet", "dessert"}},
	{6, []string{"pasta", "rice", "cereal", "flour", "condiment", "sauce", "canned", "spice", "oil", "groceries", "breakfast", "legumes-and-their-products"}},
}

// singular maps a word to a normalised singular form so plural and singular tag
// segments compare equal: "dairies"->"dairy", "creams"->"cream", "eggs"->"egg",
// "potatoes"->"potato" (-oes→-o), "fishes"->"fish" (-shes→-sh).
// Applied to a hyphenated compound it only affects the trailing word
// ("ice-creams"->"ice-cream"), which is the intended behavior.
func singular(w string) string {
	switch {
	case strings.HasSuffix(w, "ies") && len(w) > 4:
		return w[:len(w)-3] + "y"
	case strings.HasSuffix(w, "oes") && len(w) > 4:
		return w[:len(w)-2]
	case (strings.HasSuffix(w, "shes") || strings.HasSuffix(w, "ches") ||
		strings.HasSuffix(w, "xes") || strings.HasSuffix(w, "sses")) && len(w) > 4:
		return w[:len(w)-2]
	case strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss"):
		return w[:len(w)-1]
	default:
		return w
	}
}

// tagUnits normalizes an OFF category tag into its match units: the
// hyphen-separated words plus the full hyphenated compound, after dropping a
// leading language prefix. "en:ice-creams" -> ["ice", "creams", "ice-creams"];
// "en:eggplants" -> ["eggplants"].
func tagUnits(tag string) []string {
	t := strings.ToLower(tag)
	if i := strings.Index(t, ":"); i >= 0 {
		t = t[i+1:]
	}
	if t == "" {
		return nil
	}
	words := strings.Split(t, "-")
	if len(words) == 1 {
		return words
	}
	return append(words, t)
}

// keywordTokens counts the hyphen-separated tokens in a keyword; a multi-word
// keyword is more specific and beats a single-word one.
func keywordTokens(kw string) int {
	return strings.Count(kw, "-") + 1
}

// AisleForCategories returns the aisle for the most specific category keyword
// that matches any tag segment, or nil when nothing matches. A keyword matches a
// tag unit by plural-insensitive equality (not substring). Among all matches the
// keyword with the most tokens wins; ties break by taxonomy priority
// (categoryGroups order).
func AisleForCategories(tags []string) *int {
	var (
		bestAisle  int
		bestTokens int
		bestGroup  int
		matched    bool
	)
	for gi, g := range categoryGroups {
		for _, kw := range g.keywords {
			kwTok := keywordTokens(kw)
			kwSing := singular(kw)
			for _, tag := range tags {
				for _, unit := range tagUnits(tag) {
					if singular(unit) != kwSing {
						continue
					}
					if !matched || kwTok > bestTokens || (kwTok == bestTokens && gi < bestGroup) {
						matched = true
						bestAisle = g.aisle
						bestTokens = kwTok
						bestGroup = gi
					}
				}
			}
		}
	}
	if !matched {
		return nil
	}
	return &bestAisle
}
