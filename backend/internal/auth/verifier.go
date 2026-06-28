// Package auth verifies OIDC ID tokens and resolves the authenticated person.
package auth

import (
	"context"
	"errors"
)

// Claims are the verified identity fields the app needs.
type Claims struct {
	Subject string
	Issuer  string
	Email   string
}

// TokenVerifier verifies a raw bearer token and returns its claims.
type TokenVerifier interface {
	Verify(ctx context.Context, rawToken string) (Claims, error)
}

// denyVerifier rejects every token; used when no OIDC audience is configured so
// the server can still boot (auth-only endpoints return 401).
type denyVerifier struct{}

// ErrAuthDisabled is returned by the deny verifier.
var ErrAuthDisabled = errors.New("auth not configured")

func (denyVerifier) Verify(context.Context, string) (Claims, error) {
	return Claims{}, ErrAuthDisabled
}

// NewDenyVerifier returns a verifier that rejects all tokens.
func NewDenyVerifier() TokenVerifier { return denyVerifier{} }
