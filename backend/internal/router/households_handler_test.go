package router_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stroem/shopping-list/backend/internal/households"
	"github.com/stroem/shopping-list/backend/internal/router"
	"github.com/stroem/shopping-list/backend/internal/web"
)

type fakeHouseholds struct {
	created households.Household
	joinErr error
	getErr  error
	got     households.Household
}

func (f *fakeHouseholds) Create(_ context.Context, _ string, _ *string) (households.Household, error) {
	return f.created, nil
}
func (f *fakeHouseholds) JoinByCode(_ context.Context, _, _ string) (households.Household, error) {
	return f.got, f.joinErr
}
func (f *fakeHouseholds) Get(_ context.Context, _ string) (households.Household, error) {
	return f.got, f.getErr
}

// principalMW injects a fixed principal so RequireAuth passes in tests.
func principalMW(p *web.Principal) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if p != nil {
				r = r.WithContext(web.WithPrincipal(r.Context(), *p))
			}
			next.ServeHTTP(w, r)
		})
	}
}

func newHouseholdRouter(p *web.Principal, store router.HouseholdStore) http.Handler {
	return router.New(router.Deps{
		DB:             fakePinger{},
		AuthMiddleware: principalMW(p),
		Households:     store,
	})
}

func TestCreateHousehold_201WithInviteCode(t *testing.T) {
	hid := "h-1"
	p := &web.Principal{UserID: "u-1"}
	store := &fakeHouseholds{created: households.Household{ID: hid, InviteCode: "code-xyz"}}
	h := newHouseholdRouter(p, store)

	req := httptest.NewRequest(http.MethodPost, "/v1/households", strings.NewReader(`{"name":"Home"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("code = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	var got households.Household
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.InviteCode != "code-xyz" || got.ID != hid {
		t.Fatalf("body = %s", rec.Body)
	}
}

func TestCreateHousehold_NoAuth_401(t *testing.T) {
	store := &fakeHouseholds{}
	h := newHouseholdRouter(nil, store) // no principal injected
	req := httptest.NewRequest(http.MethodPost, "/v1/households", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rec.Code)
	}
}

func TestJoinHousehold_UnknownCode_404(t *testing.T) {
	p := &web.Principal{UserID: "u-1"}
	store := &fakeHouseholds{joinErr: households.ErrNotFound}
	h := newHouseholdRouter(p, store)
	req := httptest.NewRequest(http.MethodPost, "/v1/households/join", strings.NewReader(`{"invite_code":"nope"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rec.Code)
	}
}

func TestGetHousehold_NotMine_404(t *testing.T) {
	mine := "h-mine"
	p := &web.Principal{UserID: "u-1", HouseholdID: &mine}
	store := &fakeHouseholds{got: households.Household{ID: "h-other", InviteCode: "c"}}
	h := newHouseholdRouter(p, store)
	req := httptest.NewRequest(http.MethodGet, "/v1/households/h-other", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("not-mine code = %d, want 404", rec.Code)
	}
}

func TestGetHousehold_Mine_200(t *testing.T) {
	mine := "h-mine"
	p := &web.Principal{UserID: "u-1", HouseholdID: &mine}
	store := &fakeHouseholds{got: households.Household{ID: mine, InviteCode: "c"}}
	h := newHouseholdRouter(p, store)
	req := httptest.NewRequest(http.MethodGet, "/v1/households/"+mine, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mine code = %d, want 200", rec.Code)
	}
}
