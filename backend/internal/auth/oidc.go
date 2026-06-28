package auth

import (
	"context"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
)

// OIDCVerifier verifies Google/OIDC ID tokens against a configured issuer and
// audience using OIDC discovery (JWKS fetched and cached by go-oidc).
type OIDCVerifier struct {
	verifier *oidc.IDTokenVerifier
}

// NewOIDCVerifier discovers the issuer's metadata and builds a verifier bound to
// the given audience (the OAuth client id).
func NewOIDCVerifier(ctx context.Context, issuer, audience string) (*OIDCVerifier, error) {
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery for %s: %w", issuer, err)
	}
	return &OIDCVerifier{verifier: provider.Verifier(&oidc.Config{ClientID: audience})}, nil
}

// Verify validates the token's signature, issuer, audience and expiry, then
// returns its identity claims.
func (o *OIDCVerifier) Verify(ctx context.Context, rawToken string) (Claims, error) {
	idToken, err := o.verifier.Verify(ctx, rawToken)
	if err != nil {
		return Claims{}, fmt.Errorf("verify id token: %w", err)
	}
	var c struct {
		Email string `json:"email"`
	}
	_ = idToken.Claims(&c)
	return Claims{Subject: idToken.Subject, Issuer: idToken.Issuer, Email: c.Email}, nil
}
