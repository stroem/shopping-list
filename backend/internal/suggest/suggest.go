// Package suggest powers the add-item autocomplete endpoint: it ranks a
// household's own items by purchase frequency, then the generic food_catalog,
// then branded ean_mappings, all carrying an aisle for list sorting.
package suggest

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/stroem/shopping-list/backend/internal/web"
)

// Suggestion is one ranked autocomplete result.
type Suggestion struct {
	Name     string  `json:"name"`
	Aisle    *int    `json:"aisle"`
	Source   string  `json:"source"`
	ImageURL *string `json:"image_url"`
	EAN      *string `json:"ean"`
}

// Querier is the slice of pgx the service needs. *pgxpool.Pool satisfies it.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Service answers suggestion queries.
type Service struct {
	db Querier
}

// New builds a Service over the given querier.
func New(db Querier) *Service { return &Service{db: db} }

const (
	defaultLimit = 10
	maxLimit     = 25
)

func clampLimit(n int) int {
	if n < 1 {
		return defaultLimit
	}
	if n > maxLimit {
		return maxLimit
	}
	return n
}

// likeEscaper neutralizes LIKE metacharacters in user input so a q of "%" or
// "a_c" can't act as a wildcard in the prefix branch. The trigram branch keeps
// the raw q (it treats the text literally).
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// suggestSQL ranks items(0) → food_catalog(1) → ean_mappings(2), matching prefix
// or trigram, deduping by lower(name) and keeping the best source. Within items,
// purchase frequency then recency (last_purchased_at) order the results.
// $1 = raw query (trigram), $2 = household id (text-cast to uuid; NULL ⇒
// catalog-only), $3 = limit, $4 = LIKE-escaped query (prefix branch).
const suggestSQL = `
WITH matches AS (
    SELECT name, aisle, 'items' AS source, 0 AS src_rank, image_url, NULL::text AS ean,
           purchase_count, last_purchased_at,
           (name ILIKE $4 || '%' ESCAPE '\') AS is_prefix, similarity(name, $1) AS sim
    FROM items
    WHERE deleted_at IS NULL AND household_id = $2::uuid
      AND (name ILIKE $4 || '%' ESCAPE '\' OR name % $1)
    UNION ALL
    SELECT name, aisle, 'food_catalog', 1, image_url, NULL,
           0, NULL::timestamptz, (name ILIKE $4 || '%' ESCAPE '\'), similarity(name, $1)
    FROM food_catalog
    WHERE deleted_at IS NULL AND (name ILIKE $4 || '%' ESCAPE '\' OR name % $1)
    UNION ALL
    SELECT name, aisle, 'openfoodfacts', 2, image_url, ean,
           0, NULL::timestamptz, (name ILIKE $4 || '%' ESCAPE '\'), similarity(name, $1)
    FROM ean_mappings
    WHERE deleted_at IS NULL AND (name ILIKE $4 || '%' ESCAPE '\' OR name % $1)
),
ranked AS (
    SELECT DISTINCT ON (lower(name)) name, aisle, source, image_url, ean,
           src_rank, purchase_count, last_purchased_at, is_prefix, sim
    FROM matches
    ORDER BY lower(name), src_rank, purchase_count DESC,
             last_purchased_at DESC NULLS LAST, is_prefix DESC, sim DESC
)
SELECT name, aisle, source, image_url, ean
FROM ranked
ORDER BY src_rank, purchase_count DESC, last_purchased_at DESC NULLS LAST,
         is_prefix DESC, sim DESC, length(name), name
LIMIT $3`

// Suggest returns ranked suggestions for q, scoped to the household resolved from
// deviceID. Empty/whitespace q returns no results without touching the DB.
func (s *Service) Suggest(ctx context.Context, deviceID, q string, limit int) ([]Suggestion, error) {
	q = strings.TrimSpace(q)
	out := []Suggestion{}
	if q == "" {
		return out, nil
	}

	household, err := s.resolveHousehold(ctx, deviceID)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.Query(ctx, suggestSQL, q, household, clampLimit(limit), likeEscaper.Replace(q))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var sug Suggestion
		if err := rows.Scan(&sug.Name, &sug.Aisle, &sug.Source, &sug.ImageURL, &sug.EAN); err != nil {
			return nil, err
		}
		out = append(out, sug)
	}
	return out, rows.Err()
}

// resolveHousehold is the identity seam: provisionally maps X-Device-Id →
// users.household_id. #8 (Google auth + invite links) swaps this body. An unknown
// device or a user with no household returns nil ⇒ catalog-only suggestions.
func (s *Service) resolveHousehold(ctx context.Context, deviceID string) (*string, error) {
	// Prefer the authenticated household set by the auth middleware (#8). Falls
	// back to the provisional X-Device-Id path when there is no principal.
	if hid, ok := web.HouseholdID(ctx); ok {
		return &hid, nil
	}
	if deviceID == "" {
		return nil, nil
	}
	var hh *string
	err := s.db.QueryRow(ctx,
		`SELECT household_id::text FROM users WHERE device_id = $1 AND deleted_at IS NULL`,
		deviceID,
	).Scan(&hh)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return hh, nil
}
