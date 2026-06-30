package router_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stroem/shopping-list/backend/internal/lists"
	"github.com/stroem/shopping-list/backend/internal/router"
	"github.com/stroem/shopping-list/backend/internal/web"
)

type fakeLists struct {
	created    bool
	list       lists.List
	all        []lists.List
	err        error
	gotHH      string
	gotName    string
	gotArchive *bool
}

func (f *fakeLists) Upsert(_ context.Context, hh, _, name string) (lists.List, bool, error) {
	f.gotHH, f.gotName = hh, name
	return f.list, f.created, f.err
}
func (f *fakeLists) List(_ context.Context, hh string) ([]lists.List, error) {
	f.gotHH = hh
	return f.all, f.err
}
func (f *fakeLists) Get(_ context.Context, hh, _ string) (lists.List, error) {
	f.gotHH = hh
	return f.list, f.err
}
func (f *fakeLists) Update(_ context.Context, hh, _ string, name *string, archived *bool) (lists.List, error) {
	f.gotHH, f.gotArchive = hh, archived
	if name != nil {
		f.gotName = *name
	}
	return f.list, f.err
}
func (f *fakeLists) SoftDelete(_ context.Context, hh, _ string) error {
	f.gotHH = hh
	return f.err
}

func newListRouter(p *web.Principal, store router.ListStore) http.Handler {
	return router.New(router.Deps{
		DB:             fakePinger{},
		AuthMiddleware: principalMW(p),
		Lists:          store,
	})
}

const validID = "11111111-1111-1111-1111-111111111111"

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestPutList_201Create_UsesPrincipalHousehold(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	store := &fakeLists{created: true, list: lists.List{ID: validID, Name: "Groceries"}}
	h := newListRouter(p, store)

	rec := do(t, h, http.MethodPut, "/v1/lists/"+validID, `{"name":"Groceries"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("code = %d, want 201", rec.Code)
	}
	if store.gotHH != "h-1" || store.gotName != "Groceries" {
		t.Fatalf("store got hh=%q name=%q (must come from principal/body)", store.gotHH, store.gotName)
	}
}

func TestPutList_200OnIdempotentRepeat(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	store := &fakeLists{created: false, list: lists.List{ID: validID, Name: "Groceries"}}
	rec := do(t, newListRouter(p, store), http.MethodPut, "/v1/lists/"+validID, `{"name":"Groceries"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
}

func TestPutList_400OnBadUUID(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	rec := do(t, newListRouter(p, &fakeLists{}), http.MethodPut, "/v1/lists/not-a-uuid", `{"name":"x"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
}

func TestPutList_400OnEmptyName(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	rec := do(t, newListRouter(p, &fakeLists{}), http.MethodPut, "/v1/lists/"+validID, `{"name":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
}

func TestPutList_404WhenForeignID(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	store := &fakeLists{err: lists.ErrNotFound}
	rec := do(t, newListRouter(p, store), http.MethodPut, "/v1/lists/"+validID, `{"name":"x"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rec.Code)
	}
}

func TestListLists_200Array(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	store := &fakeLists{all: []lists.List{{ID: validID, Name: "Groceries"}}}
	rec := do(t, newListRouter(p, store), http.MethodGet, "/v1/lists", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	var got []lists.List
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got) != 1 {
		t.Fatalf("body = %s err=%v", rec.Body.String(), err)
	}
}

func TestListRoutes_404WhenNoHousehold(t *testing.T) {
	p := &web.Principal{UserID: "u-1"} // no household
	store := &fakeLists{list: lists.List{ID: validID}}
	// GET one → 404
	if rec := do(t, newListRouter(p, store), http.MethodGet, "/v1/lists/"+validID, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("get one no-household code = %d, want 404", rec.Code)
	}
	// GET collection → 200 empty array
	rec := do(t, newListRouter(p, store), http.MethodGet, "/v1/lists", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list no-household code = %d, want 200", rec.Code)
	}
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("list no-household body = %q, want []", rec.Body.String())
	}
}

func TestGetList_404OnNotFound(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	store := &fakeLists{err: lists.ErrNotFound}
	rec := do(t, newListRouter(p, store), http.MethodGet, "/v1/lists/"+validID, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rec.Code)
	}
}

func TestPatchList_200_AndEmptyBody400(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	store := &fakeLists{list: lists.List{ID: validID, Name: "Food"}}
	h := newListRouter(p, store)

	if rec := do(t, h, http.MethodPatch, "/v1/lists/"+validID, `{"archived":true}`); rec.Code != http.StatusOK {
		t.Fatalf("patch code = %d, want 200", rec.Code)
	}
	if store.gotArchive == nil || *store.gotArchive != true {
		t.Fatalf("archived not passed through: %v", store.gotArchive)
	}
	// Empty PATCH body (no fields) → 400.
	if rec := do(t, h, http.MethodPatch, "/v1/lists/"+validID, `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty patch code = %d, want 400", rec.Code)
	}
}

func TestDeleteList_204_And404(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}

	if rec := do(t, newListRouter(p, &fakeLists{}), http.MethodDelete, "/v1/lists/"+validID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete code = %d, want 204", rec.Code)
	}
	store := &fakeLists{err: lists.ErrNotFound}
	if rec := do(t, newListRouter(p, store), http.MethodDelete, "/v1/lists/"+validID, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("delete missing code = %d, want 404", rec.Code)
	}
}
