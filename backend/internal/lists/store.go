// Package lists persists household-scoped shopping lists.
package lists

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound means no list matched the (household, id) scope, or it was
// soft-deleted. Handlers map it to 404 — no existence leak.
var ErrNotFound = errors.New("list not found")

// List is a household-scoped shopping list.
type List struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	ArchivedAt *time.Time `json:"archived_at"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	DeletedAt  *time.Time `json:"deleted_at"`
}

// Store persists lists.
type Store struct{ db *pgxpool.Pool }

// NewStore builds a Store.
func NewStore(db *pgxpool.Pool) *Store { return &Store{db: db} }

const listCols = `id::text, name, archived_at, created_at, updated_at, deleted_at`

func scanList(row pgx.Row) (List, error) {
	var l List
	err := row.Scan(&l.ID, &l.Name, &l.ArchivedAt, &l.CreatedAt, &l.UpdatedAt, &l.DeletedAt)
	return l, err
}

// Upsert creates-or-replaces the list for (householdID, id). The bool reports
// whether a new row was inserted (true → 201) vs updated (false → 200), via the
// xmax=0 RETURNING trick. The ON CONFLICT update is guarded by household_id and
// deleted_at IS NULL, so a foreign id — or this household's own soft-deleted id —
// matches no row, RETURNING is empty, and we return ErrNotFound. Delete stays
// terminal; foreign ids never leak.
func (s *Store) Upsert(ctx context.Context, householdID, id, name string) (List, bool, error) {
	var l List
	var created bool
	err := s.db.QueryRow(ctx, `
INSERT INTO lists (id, household_id, name)
VALUES ($1::uuid, $2::uuid, $3)
ON CONFLICT (id) DO UPDATE
   SET name = EXCLUDED.name, updated_at = now()
   WHERE lists.household_id = $2::uuid AND lists.deleted_at IS NULL
RETURNING `+listCols+`, (xmax = 0) AS created`,
		id, householdID, name,
	).Scan(&l.ID, &l.Name, &l.ArchivedAt, &l.CreatedAt, &l.UpdatedAt, &l.DeletedAt, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return List{}, false, ErrNotFound
	}
	if err != nil {
		return List{}, false, fmt.Errorf("upsert list: %w", err)
	}
	return l, created, nil
}

// Get returns one non-deleted list scoped to the household, or ErrNotFound.
func (s *Store) Get(ctx context.Context, householdID, id string) (List, error) {
	l, err := scanList(s.db.QueryRow(ctx,
		`SELECT `+listCols+` FROM lists
		 WHERE id = $1::uuid AND household_id = $2::uuid AND deleted_at IS NULL`,
		id, householdID,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return List{}, ErrNotFound
	}
	if err != nil {
		return List{}, fmt.Errorf("get list: %w", err)
	}
	return l, nil
}
