package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/oauth2-proxy/mockoidc"

	"github.com/stroem/shopping-list/backend/internal/auth"
	"github.com/stroem/shopping-list/backend/internal/households"
	"github.com/stroem/shopping-list/backend/internal/router"
	"github.com/stroem/shopping-list/backend/internal/suggest"
)

type okPinger struct{}

func (okPinger) Ping(context.Context) error { return nil }

func mint(t *testing.T, m *mockoidc.MockOIDC, sub string) string {
	t.Helper()
	now := time.Now()
	tok, err := m.Keypair.SignJWT(jwt.MapClaims{
		"iss": m.Issuer(), "aud": m.Config().ClientID, "sub": sub,
		"email": sub + "@example.com", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	return tok
}

func TestHouseholdFlow_EndToEnd(t *testing.T) {
	m, err := mockoidc.Run()
	if err != nil {
		t.Fatalf("mockoidc: %v", err)
	}
	defer func() { _ = m.Shutdown() }()

	ctx := context.Background()
	pool := newPool(t) // from store_test.go (same package); skips without docker
	verifier, err := auth.NewOIDCVerifier(ctx, m.Issuer(), m.Config().ClientID)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}
	h := router.New(router.Deps{
		DB:             okPinger{},
		Suggest:        suggest.New(pool),
		AuthMiddleware: auth.Middleware(verifier, auth.NewUserStore(pool)),
		Households:     households.NewStore(pool),
	})

	call := func(method, path, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// Person A creates a household.
	recA := call(http.MethodPost, "/v1/households", mint(t, m, "personA"), `{"name":"Home"}`)
	if recA.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", recA.Code, recA.Body)
	}
	var created households.Household
	_ = json.Unmarshal(recA.Body.Bytes(), &created)
	if created.InviteCode == "" {
		t.Fatal("no invite code")
	}

	// Person B joins with the code.
	recB := call(http.MethodPost, "/v1/households/join", mint(t, m, "personB"),
		`{"invite_code":"`+created.InviteCode+`"}`)
	if recB.Code != http.StatusOK {
		t.Fatalf("join = %d: %s", recB.Code, recB.Body)
	}

	// Person C (no membership) cannot see the household → 404.
	recC := call(http.MethodGet, "/v1/households/"+created.ID, mint(t, m, "personC"), "")
	if recC.Code != http.StatusNotFound {
		t.Fatalf("stranger get = %d, want 404", recC.Code)
	}

	// Unauthenticated → 401.
	if rec := call(http.MethodPost, "/v1/households", "", `{}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no-auth create = %d, want 401", rec.Code)
	}
}
