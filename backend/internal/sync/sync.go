// Package sync implements server-side pull-sync: clients fetch household-scoped
// rows changed since an updated_at cursor (soft-deletes included) and apply them
// locally. Reads happen in one snapshot so the cursor is consistent across
// entities. No realtime push in v1.
package sync

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Result is the changed rows per entity plus the next cursor (max updated_at
// seen, or the unchanged `since` when nothing changed).
type Result struct {
	Cursor  time.Time
	Changes map[string][]map[string]any
}

// Store reads pull-sync changes from Postgres.
type Store struct{ pool *pgxpool.Pool }

// NewStore returns a Store over pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Changes returns the household's rows changed since `since` across all
// registered entities, read from a single REPEATABLE READ snapshot.
func (s *Store) Changes(ctx context.Context, householdID string, since time.Time) (Result, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Result{}, fmt.Errorf("begin sync snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	res := Result{Cursor: since, Changes: make(map[string][]map[string]any, len(registry))}
	for _, e := range registry {
		rows, err := tx.Query(ctx, e.query(), householdID, since)
		if err != nil {
			return Result{}, fmt.Errorf("query %s: %w", e.name, err)
		}
		maps, err := pgx.CollectRows(rows, pgx.RowToMap)
		if err != nil {
			return Result{}, fmt.Errorf("collect %s: %w", e.name, err)
		}
		if maps == nil {
			maps = []map[string]any{} // stable JSON shape: [] not null
		}
		res.Changes[e.name] = maps
		for _, m := range maps {
			if ts, ok := m["updated_at"].(time.Time); ok && ts.After(res.Cursor) {
				res.Cursor = ts
			}
		}
	}
	return res, nil
}
