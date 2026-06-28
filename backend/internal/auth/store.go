package auth

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// UserStore upserts the authenticated person and returns their household (if any).
type UserStore interface {
	Upsert(ctx context.Context, issuer, subject, email string) (userID string, householdID *string, err error)
}

// PgUserStore is the pgx-backed UserStore.
type PgUserStore struct{ db *pgxpool.Pool }

// NewUserStore builds a PgUserStore.
func NewUserStore(db *pgxpool.Pool) *PgUserStore { return &PgUserStore{db: db} }

const upsertUserSQL = `
INSERT INTO users (issuer, subject, email)
VALUES ($1, $2, $3)
ON CONFLICT (issuer, subject)
DO UPDATE SET email = EXCLUDED.email, updated_at = now()
RETURNING id::text, household_id::text`

// Upsert auto-creates the user on first sight, keyed by (issuer, subject).
func (s *PgUserStore) Upsert(ctx context.Context, issuer, subject, email string) (string, *string, error) {
	var id string
	var household *string
	err := s.db.QueryRow(ctx, upsertUserSQL, issuer, subject, email).Scan(&id, &household)
	if err != nil {
		return "", nil, fmt.Errorf("upsert user: %w", err)
	}
	return id, household, nil
}
