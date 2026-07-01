// Package listitems persists household-scoped list items — one occurrence of a
// product on a list — and keeps the per-household items master in step.
package listitems

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/stroem/shopping-list/backend/internal/catalog"
)

// ErrNotFound means no live row matched the (household, id) scope, or a
// referenced list/item was foreign or soft-deleted. Handlers map it to 404 —
// no existence leak.
var ErrNotFound = errors.New("list item not found")

// ListItem is one line on a household-scoped list.
type ListItem struct {
	ID        string     `json:"id"`
	ListID    string     `json:"list_id"`
	ItemID    *string    `json:"item_id"`
	Name      string     `json:"name"`
	Quantity  int        `json:"quantity"`
	Note      *string    `json:"note"`
	Aisle     *int       `json:"aisle"`
	Position  int        `json:"position"`
	CheckedAt *time.Time `json:"checked_at"`
	CheckedBy *string    `json:"checked_by"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at"`
}

// Store persists list items.
type Store struct{ db *pgxpool.Pool }

// NewStore builds a Store.
func NewStore(db *pgxpool.Pool) *Store { return &Store{db: db} }

// AddInput is the payload for adding (or replaying) a list item. A zero or
// negative Quantity defaults to 1; a nil Aisle falls back to the catalog aisle
// for Name.
type AddInput struct {
	Name     string
	Quantity int
	Note     *string
	ItemID   *string
	Aisle    *int
}

const listItemCols = `id::text, list_id::text, item_id::text, name, quantity, note, aisle,
	position, checked_at, checked_by::text, created_at, updated_at, deleted_at`

func scanListItem(row pgx.Row, created *bool) (ListItem, error) {
	var li ListItem
	dst := []any{
		&li.ID, &li.ListID, &li.ItemID, &li.Name, &li.Quantity, &li.Note, &li.Aisle,
		&li.Position, &li.CheckedAt, &li.CheckedBy, &li.CreatedAt, &li.UpdatedAt, &li.DeletedAt,
	}
	if created != nil {
		dst = append(dst, created)
	}
	err := row.Scan(dst...)
	return li, err
}

// Add creates-or-replaces the list item for (householdID, listID, id) inside one
// transaction. The bool reports whether a new row was inserted (true → 201) vs
// updated (false → 200), via the xmax=0 RETURNING trick.
//
// It first verifies the list is live and household-scoped, then — when set — that
// item_id is a live item of the same household; either miss yields ErrNotFound
// (no existence leak). Quantity defaults to 1; the aisle falls back to the
// catalog aisle for the name. On a fresh insert it bumps the household items
// master (purchase_count +1, last_purchased_at now) so replays never double-bump.
func (s *Store) Add(ctx context.Context, householdID, listID, id string, in AddInput) (ListItem, bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return ListItem{}, false, fmt.Errorf("begin add: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after a successful Commit.

	// a. Target list must be live and household-scoped.
	var ok int
	err = tx.QueryRow(ctx,
		`SELECT 1 FROM lists WHERE id = $1::uuid AND household_id = $2::uuid AND deleted_at IS NULL`,
		listID, householdID,
	).Scan(&ok)
	if errors.Is(err, pgx.ErrNoRows) {
		return ListItem{}, false, ErrNotFound
	}
	if err != nil {
		return ListItem{}, false, fmt.Errorf("verify list: %w", err)
	}

	// b. A linked item_id must be a live item of this household.
	if in.ItemID != nil {
		err = tx.QueryRow(ctx,
			`SELECT 1 FROM items WHERE id = $1::uuid AND household_id = $2::uuid AND deleted_at IS NULL`,
			*in.ItemID, householdID,
		).Scan(&ok)
		if errors.Is(err, pgx.ErrNoRows) {
			return ListItem{}, false, ErrNotFound
		}
		if err != nil {
			return ListItem{}, false, fmt.Errorf("verify item: %w", err)
		}
	}

	// c. Quantity defaults to 1; d. aisle falls back to the catalog aisle.
	quantity := in.Quantity
	if quantity <= 0 {
		quantity = 1
	}
	aisle := in.Aisle
	if aisle == nil {
		aisle = catalog.AisleFor(in.Name)
	}

	// e. Upsert the list_items row, guarding the update by household + live scope
	// so a foreign or soft-deleted id matches no row (ErrNotFound); delete stays
	// terminal. position is set only on insert, left untouched on update.
	var created bool
	li, err := scanListItem(tx.QueryRow(ctx, `
INSERT INTO list_items (id, household_id, list_id, item_id, name, quantity, note, aisle, position)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, $8, 0)
ON CONFLICT (id) DO UPDATE
   SET name = EXCLUDED.name, item_id = EXCLUDED.item_id, quantity = EXCLUDED.quantity,
       note = EXCLUDED.note, aisle = EXCLUDED.aisle, updated_at = now()
   WHERE list_items.household_id = $2::uuid AND list_items.deleted_at IS NULL
RETURNING `+listItemCols+`, (xmax = 0) AS created`,
		id, householdID, listID, in.ItemID, in.Name, quantity, in.Note, aisle,
	), &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return ListItem{}, false, ErrNotFound
	}
	if err != nil {
		return ListItem{}, false, fmt.Errorf("upsert list item: %w", err)
	}

	// f. Only a fresh insert bumps the items master; replays must not double-bump.
	if created {
		if _, err := tx.Exec(ctx, `
INSERT INTO items (household_id, name, aisle, purchase_count, last_purchased_at)
VALUES ($1::uuid, $2, $3, 1, now())
ON CONFLICT (household_id, lower(name)) WHERE deleted_at IS NULL
DO UPDATE SET purchase_count = items.purchase_count + 1,
             last_purchased_at = now(),
             updated_at = now()`,
			householdID, in.Name, aisle,
		); err != nil {
			return ListItem{}, false, fmt.Errorf("bump items master: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return ListItem{}, false, fmt.Errorf("commit add: %w", err)
	}
	return li, created, nil
}

// List returns the target list's live, household-scoped rows ordered by position
// ASC then created_at ASC. A foreign or soft-deleted list id matches no rows and
// yields a non-nil empty slice — the household scope alone excludes it, no error.
func (s *Store) List(ctx context.Context, householdID, listID string) ([]ListItem, error) {
	rows, err := s.db.Query(ctx,
		`SELECT `+listItemCols+` FROM list_items
		 WHERE list_id = $1::uuid AND household_id = $2::uuid AND deleted_at IS NULL
		 ORDER BY position ASC, created_at ASC`,
		listID, householdID,
	)
	if err != nil {
		return nil, fmt.Errorf("list list items: %w", err)
	}
	defer rows.Close()

	out := []ListItem{}
	for rows.Next() {
		li, err := scanListItem(rows, nil)
		if err != nil {
			return nil, fmt.Errorf("scan list item: %w", err)
		}
		out = append(out, li)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate list items: %w", err)
	}
	return out, nil
}

// Update patches quantity, note, and/or position on a live, household-scoped row;
// a nil argument leaves that column unchanged (COALESCE), and every call advances
// updated_at. Returns ErrNotFound if the row is absent, foreign, or soft-deleted.
func (s *Store) Update(ctx context.Context, householdID, id string, quantity *int, note *string, position *int) (ListItem, error) {
	li, err := scanListItem(s.db.QueryRow(ctx, `
UPDATE list_items SET
    quantity   = COALESCE($3, quantity),
    note       = COALESCE($4, note),
    position   = COALESCE($5, position),
    updated_at = now()
WHERE id = $1::uuid AND household_id = $2::uuid AND deleted_at IS NULL
RETURNING `+listItemCols,
		id, householdID, quantity, note, position,
	), nil)
	if errors.Is(err, pgx.ErrNoRows) {
		return ListItem{}, ErrNotFound
	}
	if err != nil {
		return ListItem{}, fmt.Errorf("update list item: %w", err)
	}
	return li, nil
}

// SoftDelete sets deleted_at on a live, household-scoped row. Returns ErrNotFound
// if no live row matched (absent, foreign, or already deleted); delete is terminal.
func (s *Store) SoftDelete(ctx context.Context, householdID, id string) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE list_items SET deleted_at = now(), updated_at = now()
		 WHERE id = $1::uuid AND household_id = $2::uuid AND deleted_at IS NULL`,
		id, householdID,
	)
	if err != nil {
		return fmt.Errorf("soft-delete list item: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
