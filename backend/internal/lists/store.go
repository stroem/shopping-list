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

// List returns the household's non-deleted lists (archived included), newest first.
func (s *Store) List(ctx context.Context, householdID string) ([]List, error) {
	rows, err := s.db.Query(ctx,
		`SELECT `+listCols+` FROM lists
		 WHERE household_id = $1::uuid AND deleted_at IS NULL
		 ORDER BY created_at DESC`,
		householdID,
	)
	if err != nil {
		return nil, fmt.Errorf("list lists: %w", err)
	}
	defer rows.Close()

	out := []List{}
	for rows.Next() {
		l, err := scanList(rows)
		if err != nil {
			return nil, fmt.Errorf("scan list: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate lists: %w", err)
	}
	return out, nil
}

// Update renames and/or (un)archives a list. A nil name leaves the name; a nil
// archived leaves the archive state. archived=true sets archived_at=now(),
// archived=false clears it. Scoped to the household and live rows only; returns
// ErrNotFound if the list is absent, foreign, or soft-deleted.
func (s *Store) Update(ctx context.Context, householdID, id string, name *string, archived *bool) (List, error) {
	l, err := scanList(s.db.QueryRow(ctx, `
UPDATE lists SET
    name        = COALESCE($3, name),
    archived_at = CASE
                    WHEN $4::boolean IS NULL THEN archived_at
                    WHEN $4::boolean THEN now()
                    ELSE NULL
                  END,
    updated_at  = now()
WHERE id = $1::uuid AND household_id = $2::uuid AND deleted_at IS NULL
RETURNING `+listCols,
		id, householdID, name, archived,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return List{}, ErrNotFound
	}
	if err != nil {
		return List{}, fmt.Errorf("update list: %w", err)
	}
	return l, nil
}

// SoftDelete sets deleted_at on a live, household-scoped list. Returns
// ErrNotFound if no live row matched (absent, foreign, or already deleted).
func (s *Store) SoftDelete(ctx context.Context, householdID, id string) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE lists SET deleted_at = now(), updated_at = now()
		 WHERE id = $1::uuid AND household_id = $2::uuid AND deleted_at IS NULL`,
		id, householdID,
	)
	if err != nil {
		return fmt.Errorf("soft-delete list: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
