// Package checkoffs records append-only check-off events — a list item being
// ticked off — and keeps the per-household items master and the list item's
// checked state in step. Check-offs feed the history that drives stats.
package checkoffs

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/stroem/shopping-list/backend/internal/catalog"
	"github.com/stroem/shopping-list/backend/internal/listitems"
)

// ErrNotFound aliases the listitems sentinel so the store's errors.Is checks and
// the router handler (which maps listitems.ErrNotFound → 404) agree on one value.
// A missing, foreign, or soft-deleted list item yields it — no existence leak.
var ErrNotFound = listitems.ErrNotFound

// Store records check-off events.
type Store struct{ db *pgxpool.Pool }

// NewStore builds a Store.
func NewStore(db *pgxpool.Pool) *Store { return &Store{db: db} }

// listItemCols mirrors listitems' unexported column list so a check-off can
// return the updated list item. Kept in sync with listitems.listItemCols.
const listItemCols = `id::text, list_id::text, item_id::text, name, quantity, note, aisle,
	position, checked_at, checked_by::text, created_at, updated_at, deleted_at`

func scanListItem(row pgx.Row) (listitems.ListItem, error) {
	var li listitems.ListItem
	err := row.Scan(
		&li.ID, &li.ListID, &li.ItemID, &li.Name, &li.Quantity, &li.Note, &li.Aisle,
		&li.Position, &li.CheckedAt, &li.CheckedBy, &li.CreatedAt, &li.UpdatedAt, &li.DeletedAt,
	)
	return li, err
}

// CheckOff ticks off a live, household-scoped list item inside one transaction:
// it appends a check_off_events row, stamps checked_at/checked_by on the list
// item, and bumps the linked item master (purchase_count +1, last_purchased_at
// now). A missing, foreign, or soft-deleted list item yields ErrNotFound (no
// existence leak).
//
// userID is the checker; an empty string records NULL. When clientEventID is
// non-nil the append dedups on (household_id, client_event_id): a replay writes
// no new event, leaves the list item and master untouched, and returns the
// current list item — so outbox replays and double-taps never double-count. A
// nil clientEventID always appends.
func (s *Store) CheckOff(ctx context.Context, householdID, id, userID string, clientEventID *string) (listitems.ListItem, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return listitems.ListItem{}, fmt.Errorf("begin check off: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after a successful Commit.

	// 1. Target list item must be live and household-scoped; capture what the
	// event records about it.
	var (
		itemID   *string
		name     string
		quantity int
	)
	err = tx.QueryRow(ctx,
		`SELECT item_id::text, name, quantity FROM list_items
		 WHERE id = $1::uuid AND household_id = $2::uuid AND deleted_at IS NULL`,
		id, householdID,
	).Scan(&itemID, &name, &quantity)
	if errors.Is(err, pgx.ErrNoRows) {
		return listitems.ListItem{}, ErrNotFound
	}
	if err != nil {
		return listitems.ListItem{}, fmt.Errorf("verify list item: %w", err)
	}

	// checked_by / user_id FK to users(id): empty userID records NULL.
	var user *string
	if userID != "" {
		user = &userID
	}

	// 2. Append the check-off event. With a client_event_id, dedup on
	// (household_id, client_event_id) — a conflict returns no row (a replay).
	if clientEventID != nil {
		var eventID string
		err = tx.QueryRow(ctx, `
INSERT INTO check_off_events (household_id, list_item_id, user_id, item_id, quantity, client_event_id)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6::uuid)
ON CONFLICT (household_id, client_event_id) WHERE client_event_id IS NOT NULL AND deleted_at IS NULL
DO NOTHING
RETURNING id::text`,
			householdID, id, user, itemID, quantity, clientEventID,
		).Scan(&eventID)
		if errors.Is(err, pgx.ErrNoRows) {
			// Replay: leave the list item and master untouched; return current.
			li, err := scanListItem(tx.QueryRow(ctx,
				`SELECT `+listItemCols+` FROM list_items
				 WHERE id = $1::uuid AND household_id = $2::uuid AND deleted_at IS NULL`,
				id, householdID,
			))
			if err != nil {
				return listitems.ListItem{}, fmt.Errorf("reselect on replay: %w", err)
			}
			if err := tx.Commit(ctx); err != nil {
				return listitems.ListItem{}, fmt.Errorf("commit check off: %w", err)
			}
			return li, nil
		}
		if err != nil {
			return listitems.ListItem{}, fmt.Errorf("append check-off event: %w", err)
		}
	} else {
		if _, err := tx.Exec(ctx, `
INSERT INTO check_off_events (household_id, list_item_id, user_id, item_id, quantity)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5)`,
			householdID, id, user, itemID, quantity,
		); err != nil {
			return listitems.ListItem{}, fmt.Errorf("append check-off event: %w", err)
		}
	}

	// 3. Stamp the list item as checked.
	li, err := scanListItem(tx.QueryRow(ctx, `
UPDATE list_items SET checked_at = now(), checked_by = $3::uuid, updated_at = now()
WHERE id = $1::uuid AND household_id = $2::uuid AND deleted_at IS NULL
RETURNING `+listItemCols,
		id, householdID, user,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return listitems.ListItem{}, ErrNotFound
	}
	if err != nil {
		return listitems.ListItem{}, fmt.Errorf("mark list item checked: %w", err)
	}

	// 4. Bump the household items master by name (mirrors listitems.Add step f).
	if _, err := tx.Exec(ctx, `
INSERT INTO items (household_id, name, aisle, purchase_count, last_purchased_at)
VALUES ($1::uuid, $2, $3, 1, now())
ON CONFLICT (household_id, lower(name)) WHERE deleted_at IS NULL
DO UPDATE SET purchase_count = items.purchase_count + 1,
             last_purchased_at = now(),
             updated_at = now()`,
		householdID, name, catalog.AisleFor(name),
	); err != nil {
		return listitems.ListItem{}, fmt.Errorf("bump items master: %w", err)
	}

	// 5. Commit and return the updated list item.
	if err := tx.Commit(ctx); err != nil {
		return listitems.ListItem{}, fmt.Errorf("commit check off: %w", err)
	}
	return li, nil
}
