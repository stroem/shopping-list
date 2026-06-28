// Package households manages household creation and invite-code membership.
package households

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound means no household matched (missing id or unknown invite code).
var ErrNotFound = errors.New("household not found")

// Household is a shared list owner.
type Household struct {
	ID         string  `json:"id"`
	Name       *string `json:"name"`
	InviteCode string  `json:"invite_code"`
}

// Store persists households.
type Store struct{ db *pgxpool.Pool }

// NewStore builds a Store.
func NewStore(db *pgxpool.Pool) *Store { return &Store{db: db} }

// Create makes a household and puts the caller in it, atomically.
func (s *Store) Create(ctx context.Context, userID string, name *string) (Household, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Household{}, err
	}
	defer tx.Rollback(ctx)

	var h Household
	if err := tx.QueryRow(ctx,
		`INSERT INTO households (name) VALUES ($1) RETURNING id::text, name, invite_code`, name,
	).Scan(&h.ID, &h.Name, &h.InviteCode); err != nil {
		return Household{}, fmt.Errorf("insert household: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE users SET household_id = $1::uuid, updated_at = now() WHERE id = $2::uuid`, h.ID, userID,
	); err != nil {
		return Household{}, fmt.Errorf("set household: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Household{}, err
	}
	return h, nil
}

// JoinByCode associates the caller with the household for invite code, if any.
// Lookup and membership update run in one transaction (parity with Create) so a
// household soft-deleted mid-call can't leave a dangling membership.
func (s *Store) JoinByCode(ctx context.Context, userID, code string) (Household, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Household{}, err
	}
	defer tx.Rollback(ctx)

	var h Household
	err = tx.QueryRow(ctx,
		`SELECT id::text, name, invite_code FROM households WHERE invite_code = $1 AND deleted_at IS NULL`, code,
	).Scan(&h.ID, &h.Name, &h.InviteCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return Household{}, ErrNotFound
	}
	if err != nil {
		return Household{}, fmt.Errorf("find household: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE users SET household_id = $1::uuid, updated_at = now() WHERE id = $2::uuid`, h.ID, userID,
	); err != nil {
		return Household{}, fmt.Errorf("join household: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Household{}, err
	}
	return h, nil
}

// Get returns the household by id, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id string) (Household, error) {
	var h Household
	err := s.db.QueryRow(ctx,
		`SELECT id::text, name, invite_code FROM households WHERE id = $1::uuid AND deleted_at IS NULL`, id,
	).Scan(&h.ID, &h.Name, &h.InviteCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return Household{}, ErrNotFound
	}
	if err != nil {
		return Household{}, fmt.Errorf("get household: %w", err)
	}
	return h, nil
}
