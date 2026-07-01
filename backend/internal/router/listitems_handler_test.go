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

// TestPutListItem_400OnZeroQuantity pins the spec's quantity >= 1 rule: an
// explicit quantity of 0 is non-positive and must be rejected with 400. The
// handler currently only rejects quantity < 0, so 0 slips through to the store.
func TestPutListItem_400OnZeroQuantity(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	// A store that would happily create a row if the handler let 0 through, so
	// the only way to reach 400 is the missing non-positive-quantity validation.
	store := &fakeListItems{created: true, item: listitems.ListItem{ID: validID, ListID: validListID, Name: "Mjölk"}}
	rec := do(t, newListItemRouter(p, store), http.MethodPut, itemPath(), `{"name":"Mjölk","quantity":0}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 (quantity 0 is non-positive)", rec.Code)
	}
}

// TestPatchListItem_400OnNegativeQuantity pins that PATCH validates quantity
// too: a negative quantity must be rejected with 400 rather than persisted. The
// PATCH handler currently forwards quantity to the store unvalidated.
func TestPatchListItem_400OnNegativeQuantity(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	// A store that would return a valid row if reached, so 400 can only come
	// from the new validation, not from a store error.
	store := &fakeListItems{item: listitems.ListItem{ID: validID, ListID: validListID, Name: "Mjölk"}}
	rec := do(t, newListItemRouter(p, store), http.MethodPatch, itemPath(), `{"quantity":-1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 (negative quantity is invalid)", rec.Code)
	}
}

// TestPatchListItem_400OnZeroQuantity pins the same quantity >= 1 rule on PATCH:
// an explicit quantity of 0 is non-positive and must be rejected with 400.
func TestPatchListItem_400OnZeroQuantity(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	store := &fakeListItems{item: listitems.ListItem{ID: validID, ListID: validListID, Name: "Mjölk"}}
	rec := do(t, newListItemRouter(p, store), http.MethodPatch, itemPath(), `{"quantity":0}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 (quantity 0 is non-positive)", rec.Code)
	}
}

// TestListListItems_400OnBadListUUID pins that a malformed {listId} in the path
// is rejected with 400 BEFORE the store is consulted. The list handler currently
// forwards the raw listId straight to the store (a real store would then 500),
// so we assert both the 400 status and that List was never called — the fake's
// List records the scope it saw, so an empty gotListID proves it was skipped.
func TestListListItems_400OnBadListUUID(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	// If the handler wrongly reached the store, List would return this row and
	// the handler would answer 200 — distinctly not the 400 we require.
	store := &fakeListItems{all: []listitems.ListItem{{ID: validID, ListID: validListID, Name: "Mjölk"}}}
	rec := do(t, newListItemRouter(p, store), http.MethodGet, "/v1/lists/not-a-uuid/items", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 (malformed list id)", rec.Code)
	}
	if store.gotListID != "" {
		t.Fatalf("store.List was called with listID=%q; want the handler to reject before reaching the store", store.gotListID)
	}
}
