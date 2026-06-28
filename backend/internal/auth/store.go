package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
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

const insertUserSQL = `
INSERT INTO users (issuer, subject, email)
VALUES ($1, $2, $3)
ON CONFLICT (issuer, subject)
DO UPDATE SET email = EXCLUDED.email, updated_at = now()
RETURNING id::text, household_id::text`

// Upsert resolves the authenticated person, auto-creating them on first sight
// (keyed by issuer, subject). The common case — an existing person — is a single
// read with no write, so repeated authenticated requests don't keep the database
// busy (scale-to-zero friendly). A write happens only on first sight or when the
// email changed.
func (s *PgUserStore) Upsert(ctx context.Context, issuer, subject, email string) (string, *string, error) {
	var id string
	var household, curEmail *string
	err := s.db.QueryRow(ctx,
		`SELECT id::text, household_id::text, email FROM users WHERE issuer = $1 AND subject = $2`,
		issuer, subject,
	).Scan(&id, &household, &curEmail)

	switch {
	case err == nil:
		if curEmail == nil || *curEmail != email {
			if _, err := s.db.Exec(ctx,
				`UPDATE users SET email = $1, updated_at = now() WHERE id = $2::uuid`, email, id,
			); err != nil {
				return "", nil, fmt.Errorf("update user email: %w", err)
			}
		}
		return id, household, nil
	case errors.Is(err, pgx.ErrNoRows):
		// First sight — insert; ON CONFLICT covers a concurrent first request.
		if err := s.db.QueryRow(ctx, insertUserSQL, issuer, subject, email).Scan(&id, &household); err != nil {
			return "", nil, fmt.Errorf("create user: %w", err)
		}
		return id, household, nil
	default:
		return "", nil, fmt.Errorf("lookup user: %w", err)
	}
}
