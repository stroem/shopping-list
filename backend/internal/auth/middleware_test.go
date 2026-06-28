package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stroem/shopping-list/backend/internal/auth"
	"github.com/stroem/shopping-list/backend/internal/web"
)

type fakeVerifier struct {
	claims auth.Claims
	err    error
}

func (f fakeVerifier) Verify(context.Context, string) (auth.Claims, error) {
	return f.claims, f.err
}

type fakeStore struct {
	uid       string
	household *string
	gotIss    string
	gotSub    string
}

func (f *fakeStore) Upsert(_ context.Context, issuer, subject, _ string) (string, *string, error) {
	f.gotIss, f.gotSub = issuer, subject
	return f.uid, f.household, nil
}

func serve(mw func(http.Handler) http.Handler, inner http.Handler, authz string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)
	return rec
}

func TestMiddleware_NoHeader_NoPrincipal(t *testing.T) {
	store := &fakeStore{uid: "u1"}
	seen := false
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if _, ok := web.PrincipalFrom(r.Context()); ok {
			t.Error("expected no principal")
		}
		seen = true
	})
	rec := serve(auth.Middleware(fakeVerifier{}, store), inner, "")
	if !seen || rec.Code != http.StatusOK {
		t.Fatalf("seen=%v code=%d", seen, rec.Code)
	}
}

func TestMiddleware_InvalidToken_401(t *testing.T) {
	mw := auth.Middleware(fakeVerifier{err: errors.New("bad")}, &fakeStore{})
	inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("inner must not run") })
	rec := serve(mw, inner, "Bearer xyz")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rec.Code)
	}
}

func TestMiddleware_ValidToken_SetsPrincipalAndUpserts(t *testing.T) {
	hh := "h-1"
	store := &fakeStore{uid: "u-9", household: &hh}
	v := fakeVerifier{claims: auth.Claims{Subject: "sub-9", Issuer: "iss-9", Email: "z@x.y"}}
	var got web.Principal
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = web.PrincipalFrom(r.Context())
	})
	rec := serve(auth.Middleware(v, store), inner, "Bearer good")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	if got.UserID != "u-9" || got.Email != "z@x.y" || got.HouseholdID == nil || *got.HouseholdID != "h-1" {
		t.Fatalf("principal = %+v", got)
	}
	if store.gotIss != "iss-9" || store.gotSub != "sub-9" {
		t.Fatalf("store got iss/sub = %q/%q", store.gotIss, store.gotSub)
	}
}

func TestRequireAuth(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	// No principal → 401.
	rec := httptest.NewRecorder()
	auth.RequireAuth(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no principal: code = %d, want 401", rec.Code)
	}

	// With principal → passes.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(web.WithPrincipal(req.Context(), web.Principal{UserID: "u1"}))
	rec = httptest.NewRecorder()
	auth.RequireAuth(inner).ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("with principal: code = %d, want 204", rec.Code)
	}
}
