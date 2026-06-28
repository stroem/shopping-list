package catalog

import "testing"

const offMilk = `{"code":"7310865004703","product_name":"Milk","product_name_sv":"Mellanmjölk",
"brands":"Arla","image_url":"http://img/milk.jpg",
"quantity":"1 L","product_quantity":"1000","product_quantity_unit":"ml",
"serving_size":"2 dl (200 ml)","serving_quantity":200,
"nutriscore_grade":"b","nova_group":1,
"categories_tags":["en:dairies","en:milks"],
"ingredients_text":"Milk","ingredients_tags":["en:milk"],
"allergens_tags":["en:milk"],"labels_tags":["en:organic"],
"nutriments":{"energy-kcal_100g":47,"fat_100g":1.5,"sugars_100g":"4.8","salt_100g":0.1}}`

const offNoName = `{"code":"123","product_name":"","product_name_sv":"  "}`

const offUnknownNutri = `{"code":"9","product_name":"Mystery","nutriscore_grade":"unknown",
"product_quantity":0,"categories_tags":["en:groceries"]}`

func TestParseOFFLine_Milk(t *testing.T) {
	row, ok, err := ParseOFFLine([]byte(offMilk))
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v, want true/nil", ok, err)
	}
	if row.EAN != "7310865004703" || row.Source != "openfoodfacts" {
		t.Fatalf("ean/source = %q/%q", row.EAN, row.Source)
	}
	if row.Name != "Mellanmjölk" { // Swedish name preferred
		t.Fatalf("name = %q, want Mellanmjölk", row.Name)
	}
	if row.Brand == nil || *row.Brand != "Arla" {
		t.Fatalf("brand = %v", row.Brand)
	}
	if row.QuantityValue == nil || *row.QuantityValue != 1000 || row.QuantityUnit == nil || *row.QuantityUnit != "ml" {
		t.Fatalf("quantity = %v %v (text %v)", row.QuantityValue, row.QuantityUnit, row.QuantityText)
	}
	if row.ServingValue == nil || *row.ServingValue != 200 {
		t.Fatalf("serving = %v", row.ServingValue)
	}
	if row.NutriscoreGrade == nil || *row.NutriscoreGrade != "b" {
		t.Fatalf("nutriscore = %v", row.NutriscoreGrade)
	}
	if row.NovaGroup == nil || *row.NovaGroup != 1 {
		t.Fatalf("nova = %v", row.NovaGroup)
	}
	if row.Aisle == nil || *row.Aisle != 2 { // en:dairies
		t.Fatalf("aisle = %v, want 2", row.Aisle)
	}
	if row.Nutriments.Fat == nil || *row.Nutriments.Fat != 1.5 {
		t.Fatalf("fat = %v", row.Nutriments.Fat)
	}
	if row.Nutriments.Sugars == nil || *row.Nutriments.Sugars != 4.8 { // string "4.8" coerced
		t.Fatalf("sugars = %v", row.Nutriments.Sugars)
	}
	if row.Nutriments.Proteins != nil { // absent → nil in fixed shape
		t.Fatalf("proteins = %v, want nil", row.Nutriments.Proteins)
	}
	if len(row.Allergens) != 1 || row.Allergens[0] != "milk" { // en: stripped
		t.Fatalf("allergens = %v", row.Allergens)
	}
	if len(row.Labels) != 1 || row.Labels[0] != "organic" {
		t.Fatalf("labels = %v", row.Labels)
	}
	if row.Ingredients.Text == nil || *row.Ingredients.Text != "Milk" ||
		len(row.Ingredients.List) != 1 || row.Ingredients.List[0] != "milk" {
		t.Fatalf("ingredients = %+v", row.Ingredients)
	}
}

func TestParseOFFLine_SkipNoName(t *testing.T) {
	_, ok, err := ParseOFFLine([]byte(offNoName))
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v, want false/nil (skip)", ok, err)
	}
}

func TestParseOFFLine_UnknownAndFallback(t *testing.T) {
	row, ok, err := ParseOFFLine([]byte(offUnknownNutri))
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if row.NutriscoreGrade != nil { // "unknown" → nil
		t.Fatalf("nutriscore = %v, want nil", row.NutriscoreGrade)
	}
	if row.QuantityValue == nil || *row.QuantityValue != 0 { // numeric 0 parses
		t.Fatalf("quantity_value = %v, want 0", row.QuantityValue)
	}
	if row.Allergens == nil || len(row.Allergens) != 0 { // never nil, empty slice
		t.Fatalf("allergens = %v, want []", row.Allergens)
	}
	if row.Aisle == nil || *row.Aisle != 6 { // en:groceries → pantry
		t.Fatalf("aisle = %v, want 6", row.Aisle)
	}
}

func TestParseOFFLine_Malformed(t *testing.T) {
	if _, _, err := ParseOFFLine([]byte(`{not json`)); err == nil {
		t.Fatal("want error on malformed JSON")
	}
}
