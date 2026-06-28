package catalog

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Nutriments is the fixed per-100g nutrition shape; every key always present,
// value a number or null.
type Nutriments struct {
	EnergyKcal    *float64 `json:"energy_kcal"`
	Fat           *float64 `json:"fat"`
	SaturatedFat  *float64 `json:"saturated_fat"`
	Carbohydrates *float64 `json:"carbohydrates"`
	Sugars        *float64 `json:"sugars"`
	Fiber         *float64 `json:"fiber"`
	Proteins      *float64 `json:"proteins"`
	Salt          *float64 `json:"salt"`
}

// Ingredients is the fixed ingredients shape: free text plus a token list.
type Ingredients struct {
	Text *string  `json:"text"`
	List []string `json:"list"`
}

// EanRow is one ean_mappings record to upsert.
type EanRow struct {
	EAN             string
	Name            string
	Brand           *string
	ImageURL        *string
	QuantityText    *string
	QuantityValue   *float64
	QuantityUnit    *string
	ServingText     *string
	ServingValue    *float64
	NutriscoreGrade *string
	NovaGroup       *int
	Nutriments      Nutriments
	Ingredients     Ingredients
	Allergens       []string
	Labels          []string
	Aisle           *int
	Source          string
}

// flexNum tolerates OFF's mixed encoding: a field may arrive as a JSON number
// (7788.0) or a JSON string ("0"). Empty/null/garbage leaves it unset.
type flexNum struct {
	set bool
	val float64
}

func (f *flexNum) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil // tolerate junk → unset, never fail the whole line
	}
	f.set, f.val = true, v
	return nil
}

func (f flexNum) floatPtr() *float64 {
	if !f.set {
		return nil
	}
	v := f.val
	return &v
}

type offProduct struct {
	Code                string             `json:"code"`
	ProductName         string             `json:"product_name"`
	ProductNameSv       string             `json:"product_name_sv"`
	Brands              string             `json:"brands"`
	ImageURL            string             `json:"image_url"`
	ImageSmallURL       string             `json:"image_small_url"`
	Quantity            string             `json:"quantity"`
	ProductQuantity     flexNum            `json:"product_quantity"`
	ProductQuantityUnit string             `json:"product_quantity_unit"`
	ServingSize         string             `json:"serving_size"`
	ServingQuantity     flexNum            `json:"serving_quantity"`
	NutriscoreGrade     string             `json:"nutriscore_grade"`
	NovaGroup           flexNum            `json:"nova_group"`
	CategoriesTags      []string           `json:"categories_tags"`
	IngredientsText     string             `json:"ingredients_text"`
	IngredientsTags     []string           `json:"ingredients_tags"`
	AllergensTags       []string           `json:"allergens_tags"`
	LabelsTags          []string           `json:"labels_tags"`
	Nutriments          map[string]flexNum `json:"nutriments"`
}

// ParseOFFLine decodes one Open Food Facts JSONL line into a normalized EanRow.
// Returns ok=false to signal a skip (empty name); err only on malformed JSON.
func ParseOFFLine(line []byte) (EanRow, bool, error) {
	var p offProduct
	if err := json.Unmarshal(line, &p); err != nil {
		return EanRow{}, false, err
	}

	name := strings.TrimSpace(p.ProductNameSv)
	if name == "" {
		name = strings.TrimSpace(p.ProductName)
	}
	// Skip rows missing a name or a code: ean is the ean_mappings primary key,
	// so an empty code would silently collapse every code-less product onto one
	// "" row (last-writer-wins) instead of failing.
	ean := strings.TrimSpace(p.Code)
	if name == "" || ean == "" {
		return EanRow{}, false, nil
	}

	row := EanRow{
		EAN:          ean,
		Name:         name,
		Brand:        strp(p.Brands),
		ImageURL:     strp(firstNonEmpty(p.ImageURL, p.ImageSmallURL)),
		QuantityText: strp(p.Quantity),
		ServingText:  strp(p.ServingSize),
		Source:       "openfoodfacts",
		Allergens:    stripPrefixAll(p.AllergensTags),
		Labels:       stripPrefixAll(p.LabelsTags),
		Ingredients: Ingredients{
			Text: strp(p.IngredientsText),
			List: stripPrefixAll(p.IngredientsTags),
		},
		Nutriments: Nutriments{
			EnergyKcal:    p.Nutriments["energy-kcal_100g"].floatPtr(),
			Fat:           p.Nutriments["fat_100g"].floatPtr(),
			SaturatedFat:  p.Nutriments["saturated-fat_100g"].floatPtr(),
			Carbohydrates: p.Nutriments["carbohydrates_100g"].floatPtr(),
			Sugars:        p.Nutriments["sugars_100g"].floatPtr(),
			Fiber:         p.Nutriments["fiber_100g"].floatPtr(),
			Proteins:      p.Nutriments["proteins_100g"].floatPtr(),
			Salt:          p.Nutriments["salt_100g"].floatPtr(),
		},
		QuantityValue:   p.ProductQuantity.floatPtr(),
		QuantityUnit:    normUnit(p.ProductQuantityUnit),
		ServingValue:    p.ServingQuantity.floatPtr(),
		NutriscoreGrade: normGrade(p.NutriscoreGrade),
		NovaGroup:       normNova(p.NovaGroup),
	}
	if a := AisleForCategories(p.CategoriesTags); a != nil {
		row.Aisle = a
	} else {
		row.Aisle = AisleFor(name)
	}
	return row, true, nil
}

func strp(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// stripPrefixAll removes the leading "<lang>:" (e.g. "en:", "xx:") from each tag
// and returns a non-nil slice (so JSON marshals to [] not null).
func stripPrefixAll(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if i := strings.IndexByte(t, ':'); i >= 0 {
			t = t[i+1:]
		}
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

func normUnit(u string) *string {
	u = strings.ToLower(strings.TrimSpace(u))
	if u == "g" || u == "ml" {
		return &u
	}
	return nil
}

func normGrade(g string) *string {
	g = strings.ToLower(strings.TrimSpace(g))
	switch g {
	case "a", "b", "c", "d", "e":
		return &g
	default:
		return nil
	}
}

func normNova(n flexNum) *int {
	if !n.set {
		return nil
	}
	i := int(n.val)
	if i < 1 || i > 4 {
		return nil
	}
	return &i
}
