package idempotency

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBStore is the pgx-backed Store.
type DBStore struct{ pool *pgxpool.Pool }

// NewStore returns a DBStore over pool.
func NewStore(pool *pgxpool.Pool) *DBStore { return &DBStore{pool: pool} }

// Lookup returns the stored response for (householdID, key), or found=false.
func (s *DBStore) Lookup(ctx context.Context, householdID, key string) (*Response, bool, error) {
	var resp Response
	err := s.pool.QueryRow(ctx,
		`SELECT status_code, response_body FROM idempotency_keys WHERE household_id = $1::uuid AND key = $2`,
		householdID, key).Scan(&resp.StatusCode, &resp.Body)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("lookup idempotency key: %w", err)
	}
	return &resp, true, nil
}

// Save stores the response for (householdID, key); a duplicate key is a no-op.
func (s *DBStore) Save(ctx context.Context, householdID, key, method, path string, status int, body []byte) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO idempotency_keys (household_id, key, method, path, status_code, response_body)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6)
		 ON CONFLICT (household_id, key) DO NOTHING`,
		householdID, key, method, path, status, body)
	if err != nil {
		return fmt.Errorf("save idempotency key: %w", err)
	}
	return nil
}
