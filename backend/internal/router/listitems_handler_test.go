package router_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stroem/shopping-list/backend/internal/listitems"
	"github.com/stroem/shopping-list/backend/internal/router"
	"github.com/stroem/shopping-list/backend/internal/web"
)

// validListID is a second distinct UUID for the {listId} path param; validID
// (from lists_handler_test.go, same package) serves as the {id}.
const validListID = "22222222-2222-2222-2222-222222222222"

// fakeListItems implements router.ListItemStore, capturing the household + list
// scope and the passed-through inputs so tests can assert the handler forwards
// the principal's household (never the client's) and the parsed body.
type fakeListItems struct {
	created   bool
	item      listitems.ListItem
	all       []listitems.ListItem
	err       error
	gotHH     string
	gotListID string
	gotIn     listitems.AddInput
	gotQty    *int
	gotNote   *string
	gotPos    *int
}

func (f *fakeListItems) Add(_ context.Context, hh, listID, _ string, in listitems.AddInput) (listitems.ListItem, bool, error) {
	f.gotHH, f.gotListID, f.gotIn = hh, listID, in
	return f.item, f.created, f.err
}
func (f *fakeListItems) List(_ context.Context, hh, listID string) ([]listitems.ListItem, error) {
	f.gotHH, f.gotListID = hh, listID
	return f.all, f.err
}
func (f *fakeListItems) Update(_ context.Context, hh, _ string, quantity *int, note *string, position *int) (listitems.ListItem, error) {
	f.gotHH, f.gotQty, f.gotNote, f.gotPos = hh, quantity, note, position
	return f.item, f.err
}
func (f *fakeListItems) SoftDelete(_ context.Context, hh, _ string) error {
	f.gotHH = hh
	return f.err
}

func newListItemRouter(p *web.Principal, store router.ListItemStore) http.Handler {
	return router.New(router.Deps{
		DB:             fakePinger{},
		AuthMiddleware: principalMW(p),
		ListItems:      store,
	})
}

func itemsPath() string { return "/v1/lists/" + validListID + "/items" }
func itemPath() string  { return "/v1/lists/" + validListID + "/items/" + validID }

func TestPutListItem_201Create_UsesPrincipalHouseholdAndBody(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	store := &fakeListItems{created: true, item: listitems.ListItem{ID: validID, ListID: validListID, Name: "Mjölk"}}
	h := newListItemRouter(p, store)

	rec := do(t, h, http.MethodPut, itemPath(), `{"name":"Mjölk","quantity":2,"note":"cold"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("code = %d, want 201", rec.Code)
	}
	if store.gotHH != "h-1" {
		t.Fatalf("store got hh=%q, want the principal's household h-1", store.gotHH)
	}
	if store.gotListID != validListID {
		t.Fatalf("store got listID=%q, want %q from path", store.gotListID, validListID)
	}
	if store.gotIn.Name != "Mjölk" || store.gotIn.Quantity != 2 {
		t.Fatalf("store got name=%q quantity=%d, want Mjölk/2", store.gotIn.Name, store.gotIn.Quantity)
	}
	if store.gotIn.Note == nil || *store.gotIn.Note != "cold" {
		t.Fatalf("store got note=%v, want \"cold\"", store.gotIn.Note)
	}
}

func TestPutListItem_200OnIdempotentReplay(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	store := &fakeListItems{created: false, item: listitems.ListItem{ID: validID, ListID: validListID, Name: "Mjölk"}}
	rec := do(t, newListItemRouter(p, store), http.MethodPut, itemPath(), `{"name":"Mjölk"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
}

func TestPutListItem_400OnBadItemUUID(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	rec := do(t, newListItemRouter(p, &fakeListItems{}), http.MethodPut,
		"/v1/lists/"+validListID+"/items/not-a-uuid", `{"name":"x"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
}

func TestPutListItem_400OnBadListUUID(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	rec := do(t, newListItemRouter(p, &fakeListItems{}), http.MethodPut,
		"/v1/lists/not-a-uuid/items/"+validID, `{"name":"x"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
}

func TestPutListItem_400OnEmptyName(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	rec := do(t, newListItemRouter(p, &fakeListItems{}), http.MethodPut, itemPath(), `{"name":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
}

func TestPutListItem_400OnNegativeQuantity(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	rec := do(t, newListItemRouter(p, &fakeListItems{}), http.MethodPut, itemPath(), `{"name":"x","quantity":-1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
}

func TestPutListItem_404WhenStoreNotFound(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	store := &fakeListItems{err: listitems.ErrNotFound}
	rec := do(t, newListItemRouter(p, store), http.MethodPut, itemPath(), `{"name":"x"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rec.Code)
	}
}

func TestListListItems_200Array(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	store := &fakeListItems{all: []listitems.ListItem{
		{ID: validID, ListID: validListID, Name: "Mjölk"},
		{ID: "33333333-3333-3333-3333-333333333333", ListID: validListID, Name: "Bröd"},
	}}
	rec := do(t, newListItemRouter(p, store), http.MethodGet, itemsPath(), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	if store.gotListID != validListID {
		t.Fatalf("store got listID=%q, want %q from path", store.gotListID, validListID)
	}
	var got []listitems.ListItem
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got) != 2 {
		t.Fatalf("body = %s (len=%d) err=%v, want 2 rows", rec.Body.String(), len(got), err)
	}
}

func TestListListItems_200EmptyArrayWhenNoHousehold(t *testing.T) {
	p := &web.Principal{UserID: "u-1"} // no household
	store := &fakeListItems{all: []listitems.ListItem{{ID: validID}}}
	rec := do(t, newListItemRouter(p, store), http.MethodGet, itemsPath(), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	var got []listitems.ListItem
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got) != 0 {
		t.Fatalf("body = %s (len=%d), want empty array (no household leaks nothing)", rec.Body.String(), len(got))
	}
}

func TestListItemSingleRoutes_404WhenNoHousehold(t *testing.T) {
	p := &web.Principal{UserID: "u-1"} // no household
	store := &fakeListItems{item: listitems.ListItem{ID: validID}}
	if rec := do(t, newListItemRouter(p, store), http.MethodPatch, itemPath(), `{"quantity":2}`); rec.Code != http.StatusNotFound {
		t.Fatalf("patch no-household code = %d, want 404", rec.Code)
	}
	if rec := do(t, newListItemRouter(p, store), http.MethodDelete, itemPath(), ""); rec.Code != http.StatusNotFound {
		t.Fatalf("delete no-household code = %d, want 404", rec.Code)
	}
}

func TestPatchListItem_200PassesThroughFields(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	store := &fakeListItems{item: listitems.ListItem{ID: validID, ListID: validListID, Name: "Mjölk"}}
	h := newListItemRouter(p, store)

	rec := do(t, h, http.MethodPatch, itemPath(), `{"quantity":3,"note":"cold","position":2}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	if store.gotQty == nil || *store.gotQty != 3 {
		t.Fatalf("quantity not passed through: %v", store.gotQty)
	}
	if store.gotNote == nil || *store.gotNote != "cold" {
		t.Fatalf("note not passed through: %v", store.gotNote)
	}
	if store.gotPos == nil || *store.gotPos != 2 {
		t.Fatalf("position not passed through: %v", store.gotPos)
	}
}

func TestPatchListItem_400OnEmptyBody(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	store := &fakeListItems{item: listitems.ListItem{ID: validID}}
	rec := do(t, newListItemRouter(p, store), http.MethodPatch, itemPath(), `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
}

func TestPatchListItem_404OnNotFound(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	store := &fakeListItems{err: listitems.ErrNotFound}
	rec := do(t, newListItemRouter(p, store), http.MethodPatch, itemPath(), `{"quantity":2}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rec.Code)
	}
}

func TestDeleteListItem_204(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	rec := do(t, newListItemRouter(p, &fakeListItems{}), http.MethodDelete, itemPath(), "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d, want 204", rec.Code)
	}
}

func TestDeleteListItem_404OnNotFound(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	store := &fakeListItems{err: listitems.ErrNotFound}
	rec := do(t, newListItemRouter(p, store), http.MethodDelete, itemPath(), "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rec.Code)
	}
}
