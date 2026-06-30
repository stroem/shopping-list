package catalog

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Row is one food_catalog record to upsert.
type Row struct {
	Source     string
	ExternalID string
	Name       string
	FoodGroup  *string
	Aisle      *int
}

// Querier is the slice of pgx used by UpsertFood. *pgxpool.Pool satisfies it.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const upsertFoodSQL = `
INSERT INTO food_catalog (source, external_id, name, food_group, aisle)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (source, external_id)
DO UPDATE SET name = EXCLUDED.name, food_group = EXCLUDED.food_group, aisle = EXCLUDED.aisle, updated_at = now()
RETURNING (xmax = 0) AS inserted`

// UpsertFood inserts or updates each row, idempotently, keyed by
// (source, external_id). It returns how many rows were newly inserted vs updated.
// The `xmax = 0` trick distinguishes a fresh insert from an update.
func UpsertFood(ctx context.Context, db Querier, rows []Row) (inserted, updated int, err error) {
	for _, r := range rows {
		var wasInsert bool
		if err = db.QueryRow(ctx, upsertFoodSQL, r.Source, r.ExternalID, r.Name, r.FoodGroup, r.Aisle).Scan(&wasInsert); err != nil {
			return inserted, updated, fmt.Errorf("upsert %s/%s: %w", r.Source, r.ExternalID, err)
		}
		if wasInsert {
			inserted++
		} else {
			updated++
		}
	}
	return inserted, updated, nil
}

const upsertEANSQL = `
INSERT INTO ean_mappings (
    ean, name, brand, aisle, image_url, source,
    quantity_text, quantity_value, quantity_unit,
    serving_text, serving_value, nutriscore_grade, nova_group,
    nutriments, ingredients, allergens, labels
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9,
    $10, $11, $12, $13,
    $14, $15, $16, $17
)
ON CONFLICT (ean) DO UPDATE SET
    name = EXCLUDED.name, brand = EXCLUDED.brand, aisle = EXCLUDED.aisle,
    image_url = EXCLUDED.image_url, source = EXCLUDED.source,
    quantity_text = EXCLUDED.quantity_text, quantity_value = EXCLUDED.quantity_value,
    quantity_unit = EXCLUDED.quantity_unit, serving_text = EXCLUDED.serving_text,
    serving_value = EXCLUDED.serving_value, nutriscore_grade = EXCLUDED.nutriscore_grade,
    nova_group = EXCLUDED.nova_group, nutriments = EXCLUDED.nutriments,
    ingredients = EXCLUDED.ingredients, allergens = EXCLUDED.allergens,
    labels = EXCLUDED.labels, updated_at = now()
RETURNING (xmax = 0) AS inserted`

// UpsertEAN inserts or updates each OFF product idempotently, keyed by ean.
// Returns how many rows were newly inserted vs updated (xmax = 0 ⇒ insert).
func UpsertEAN(ctx context.Context, db Querier, rows []EanRow) (inserted, updated int, err error) {
	for _, r := range rows {
		nutriments, _ := json.Marshal(r.Nutriments)
		ingredients, _ := json.Marshal(r.Ingredients)
		allergens, _ := json.Marshal(r.Allergens)
		labels, _ := json.Marshal(r.Labels)

		var wasInsert bool
		err = db.QueryRow(ctx, upsertEANSQL,
			r.EAN, r.Name, r.Brand, r.Aisle, r.ImageURL, r.Source,
			r.QuantityText, r.QuantityValue, r.QuantityUnit,
			r.ServingText, r.ServingValue, r.NutriscoreGrade, r.NovaGroup,
			nutriments, ingredients, allergens, labels,
		).Scan(&wasInsert)
		if err != nil {
			return inserted, updated, fmt.Errorf("upsert ean %s: %w", r.EAN, err)
		}
		if wasInsert {
			inserted++
		} else {
			updated++
		}
	}
	return inserted, updated, nil
}
