package catalog

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Row is one food_catalog record to upsert.
type Row struct {
	Source     string
	ExternalID string
	Name       string
	Aisle      *int
}

// Querier is the slice of pgx used by UpsertFood. *pgxpool.Pool satisfies it.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const upsertFoodSQL = `
INSERT INTO food_catalog (source, external_id, name, aisle)
VALUES ($1, $2, $3, $4)
ON CONFLICT (source, external_id)
DO UPDATE SET name = EXCLUDED.name, aisle = EXCLUDED.aisle, updated_at = now()
RETURNING (xmax = 0) AS inserted`

// UpsertFood inserts or updates each row, idempotently, keyed by
// (source, external_id). It returns how many rows were newly inserted vs updated.
// The `xmax = 0` trick distinguishes a fresh insert from an update.
func UpsertFood(ctx context.Context, db Querier, rows []Row) (inserted, updated int, err error) {
	for _, r := range rows {
		var wasInsert bool
		if err = db.QueryRow(ctx, upsertFoodSQL, r.Source, r.ExternalID, r.Name, r.Aisle).Scan(&wasInsert); err != nil {
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
