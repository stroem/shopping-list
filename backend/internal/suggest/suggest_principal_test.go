package suggest

import (
	"context"
	"testing"

	"github.com/stroem/shopping-list/backend/internal/web"
)

func TestResolveHousehold_PrefersPrincipal(t *testing.T) {
	s := New(nil) // db unused: the principal short-circuits before any query
	hh := "h-principal"
	ctx := web.WithPrincipal(context.Background(), web.Principal{UserID: "u", HouseholdID: &hh})

	got, err := s.resolveHousehold(ctx, "ignored-device")
	if err != nil || got == nil || *got != "h-principal" {
		t.Fatalf("resolveHousehold = %v err=%v, want h-principal", got, err)
	}
}
