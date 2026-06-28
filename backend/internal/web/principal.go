package web

import "context"

// Principal is the authenticated caller, set by the auth middleware.
type Principal struct {
	UserID      string
	HouseholdID *string
	Email       string
}

type principalKey struct{}

// WithPrincipal returns a context carrying p.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the principal set by the auth middleware, if any.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// HouseholdID returns the caller's household id, or ("", false) when there is no
// principal or the principal has no household yet.
func HouseholdID(ctx context.Context) (string, bool) {
	p, ok := PrincipalFrom(ctx)
	if !ok || p.HouseholdID == nil {
		return "", false
	}
	return *p.HouseholdID, true
}
