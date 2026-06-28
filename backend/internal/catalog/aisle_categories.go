package catalog

import "strings"

// categoryGroups maps OFF English category-tag substrings to the v1 aisle
// taxonomy, same priority order as AisleFor: specific proteins first, pantry
// last as the catch-all. Tags look like "en:dairies", "en:barbecue-sauces".
var categoryGroups = []aisleGroup{
	{4, []string{"seafood", "fish", "salmon", "tuna", "shellfish", "shrimp", "herring", "mackerel"}},
	{3, []string{"meat", "poultry", "beef", "pork", "chicken", "ham", "sausage", "bacon", "charcuterie", "turkey"}},
	{2, []string{"dairies", "dairy", "milk", "cheese", "yogurt", "yoghurt", "butter", "cream", "egg"}},
	{1, []string{"fruit", "vegetable", "legume", "salad", "potato", "berries", "mushroom"}},
	{5, []string{"bread", "bakery", "viennoiserie", "baguette", "toast", "crackers"}},
	{7, []string{"frozen", "ice-cream", "ice-creams"}},
	{8, []string{"beverage", "water", "juice", "soda", "coffee", "tea", "drink", "wine", "beer", "alcoholic", "spirit"}},
	{9, []string{"chocolate", "candy", "candies", "snack", "biscuit", "chips", "confectioner", "sweet", "dessert"}},
	{6, []string{"pasta", "rice", "cereal", "flour", "condiment", "sauce", "canned", "spice", "oil", "groceries", "breakfast", "legumes-and-their-products"}},
}

// AisleForCategories returns the first aisle whose keyword is a substring of any
// category tag, by taxonomy priority, or nil when nothing matches.
func AisleForCategories(tags []string) *int {
	for _, g := range categoryGroups {
		for _, kw := range g.keywords {
			for _, tag := range tags {
				if strings.Contains(strings.ToLower(tag), kw) {
					a := g.aisle
					return &a
				}
			}
		}
	}
	return nil
}
