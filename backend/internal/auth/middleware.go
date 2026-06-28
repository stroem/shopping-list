package auth

import (
	"net/http"
	"strings"

	"github.com/stroem/shopping-list/backend/internal/web"
)

// Middleware is optional auth: when an "Authorization: Bearer <token>" header is
// present it verifies the token, upserts the user, and puts a web.Principal in
// the context. An invalid token is a 401; an absent header passes through with no
// principal (open endpoints still work).
func Middleware(v TokenVerifier, store UserStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := bearer(r)
			if raw == "" {
				next.ServeHTTP(w, r)
				return
			}
			claims, err := v.Verify(r.Context(), raw)
			if err != nil {
				web.Error(w, http.StatusUnauthorized, "invalid token")
				return
			}
			userID, household, err := store.Upsert(r.Context(), claims.Issuer, claims.Subject, claims.Email)
			if err != nil {
				web.Error(w, http.StatusInternalServerError, "auth store failed")
				return
			}
			ctx := web.WithPrincipal(r.Context(), web.Principal{
				UserID: userID, HouseholdID: household, Email: claims.Email,
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAuth rejects requests without a principal (401). Use it to guard
// endpoints that need an authenticated caller.
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := web.PrincipalFrom(r.Context()); !ok {
			web.Error(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const p = "Bearer "
	if len(h) > len(p) && strings.EqualFold(h[:len(p)], p) {
		return strings.TrimSpace(h[len(p):])
	}
	return ""
}
