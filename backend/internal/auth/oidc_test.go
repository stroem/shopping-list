package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/oauth2-proxy/mockoidc"

	"github.com/stroem/shopping-list/backend/internal/auth"
)

func TestOIDCVerifier_VerifiesMintedToken(t *testing.T) {
	m, err := mockoidc.Run()
	if err != nil {
		t.Fatalf("mockoidc: %v", err)
	}
	defer func() { _ = m.Shutdown() }()

	ctx := context.Background()
	v, err := auth.NewOIDCVerifier(ctx, m.Issuer(), m.Config().ClientID)
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}

	now := time.Now()
	tok, err := m.Keypair.SignJWT(jwt.MapClaims{
		"iss":   m.Issuer(),
		"aud":   m.Config().ClientID,
		"sub":   "google-sub-123",
		"email": "person@example.com",
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	claims, err := v.Verify(ctx, tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.Subject != "google-sub-123" || claims.Email != "person@example.com" || claims.Issuer != m.Issuer() {
		t.Fatalf("claims = %+v", claims)
	}

	// Wrong audience must fail.
	bad, _ := m.Keypair.SignJWT(jwt.MapClaims{
		"iss": m.Issuer(), "aud": "someone-else",
		"sub": "x", "exp": now.Add(time.Hour).Unix(),
	})
	if _, err := v.Verify(ctx, bad); err == nil {
		t.Fatal("expected wrong-audience token to fail verification")
	}
}
