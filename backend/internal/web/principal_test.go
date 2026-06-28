package web

import (
	"context"
	"testing"
)

func TestPrincipalRoundTrip(t *testing.T) {
	ctx := context.Background()
	if _, ok := PrincipalFrom(ctx); ok {
		t.Fatal("empty ctx should have no principal")
	}
	if _, ok := HouseholdID(ctx); ok {
		t.Fatal("empty ctx should have no household")
	}

	hh := "11111111-1111-1111-1111-111111111111"
	ctx = WithPrincipal(ctx, Principal{UserID: "u1", HouseholdID: &hh, Email: "a@b.c"})

	p, ok := PrincipalFrom(ctx)
	if !ok || p.UserID != "u1" || p.Email != "a@b.c" || p.HouseholdID == nil || *p.HouseholdID != hh {
		t.Fatalf("principal = %+v ok=%v", p, ok)
	}
	if id, ok := HouseholdID(ctx); !ok || id != hh {
		t.Fatalf("household = %q ok=%v", id, ok)
	}

	// A principal with no household → HouseholdID false.
	ctx2 := WithPrincipal(context.Background(), Principal{UserID: "u2"})
	if _, ok := HouseholdID(ctx2); ok {
		t.Fatal("no-household principal should give HouseholdID ok=false")
	}
}
