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

// fakeCheckOffs implements router.CheckOffStore, capturing the scope + inputs the
// handler forwards so tests can assert it passes the principal's household (never
// a client value), the principal's user id, and the parsed client_event_id.
type fakeCheckOffs struct {
	item             listitems.ListItem
	err              error
	gotHH            string
	gotID            string
	gotUserID        string
	gotClientEventID *string
}

func (f *fakeCheckOffs) CheckOff(_ context.Context, householdID, id, userID string, clientEventID *string) (listitems.ListItem, error) {
	f.gotHH, f.gotID, f.gotUserID, f.gotClientEventID = householdID, id, userID, clientEventID
	return f.item, f.err
}

func newCheckOffRouter(p *web.Principal, store router.CheckOffStore) http.Handler {
	return router.New(router.Deps{
		DB:             fakePinger{},
		AuthMiddleware: principalMW(p),
		CheckOffs:      store,
	})
}

// checkOffPath is the nested check-off route for the shared validListID/validID.
func checkOffPath() string {
	return "/v1/lists/" + validListID + "/items/" + validID + "/check-off"
}

// TestCheckOff_200UsesPrincipalHouseholdAndBody pins the happy path: the handler
// returns 200 with the updated list item and forwards the principal's household
// and user id plus the body's client_event_id to the store — never a client value
// for the household.
func TestCheckOff_200UsesPrincipalHouseholdAndBody(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	checkedBy := "u-1"
	store := &fakeCheckOffs{item: listitems.ListItem{
		ID:        validID,
		ListID:    validListID,
		Name:      "Mjölk",
		CheckedBy: &checkedBy,
	}}

	const eventID = "33333333-3333-3333-3333-333333333333"
	rec := do(t, newCheckOffRouter(p, store), http.MethodPost, checkOffPath(),
		`{"client_event_id":"`+eventID+`"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	if store.gotHH != "h-1" {
		t.Fatalf("store got hh=%q, want the principal's household h-1", store.gotHH)
	}
	if store.gotID != validID {
		t.Fatalf("store got id=%q, want %q from path", store.gotID, validID)
	}
	if store.gotUserID != "u-1" {
		t.Fatalf("store got userID=%q, want the principal's u-1", store.gotUserID)
	}
	if store.gotClientEventID == nil || *store.gotClientEventID != eventID {
		t.Fatalf("store got client_event_id=%v, want %q", store.gotClientEventID, eventID)
	}
	var got listitems.ListItem
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body %s: %v", rec.Body.String(), err)
	}
	if got.ID != validID || got.CheckedBy == nil || *got.CheckedBy != "u-1" {
		t.Fatalf("body = %s, want the checked-off list item (id=%s, checked_by=u-1)", rec.Body.String(), validID)
	}
}

// TestCheckOff_200NilClientEventID pins that an absent client_event_id is
// forwarded as nil (append-only, no DB-level dedup), still answering 200.
func TestCheckOff_200NilClientEventID(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	store := &fakeCheckOffs{item: listitems.ListItem{ID: validID, ListID: validListID, Name: "Mjölk"}}

	rec := do(t, newCheckOffRouter(p, store), http.MethodPost, checkOffPath(), `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	if store.gotClientEventID != nil {
		t.Fatalf("store got client_event_id=%v, want nil when the body omits it", *store.gotClientEventID)
	}
}

// TestCheckOff_404WhenNoHousehold pins the household boundary: a principal with no
// household must get 404 (no existence leak) and must never reach the store.
func TestCheckOff_404WhenNoHousehold(t *testing.T) {
	p := &web.Principal{UserID: "u-1"} // no household
	store := &fakeCheckOffs{item: listitems.ListItem{ID: validID}}

	rec := do(t, newCheckOffRouter(p, store), http.MethodPost, checkOffPath(),
		`{"client_event_id":"33333333-3333-3333-3333-333333333333"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rec.Code)
	}
	if store.gotUserID != "" {
		t.Fatalf("store was called (gotUserID=%q); want the handler to 404 before reaching the store", store.gotUserID)
	}
}

// TestCheckOff_404WhenStoreNotFound pins that a missing/foreign list item —
// surfaced as listitems.ErrNotFound from the store — maps to 404. The router does
// not import checkoffs (plan invariant), so the sentinel it maps is the shared
// listitems.ErrNotFound, mirroring the PUT/PATCH/DELETE list-item handlers.
func TestCheckOff_404WhenStoreNotFound(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	store := &fakeCheckOffs{err: listitems.ErrNotFound}

	rec := do(t, newCheckOffRouter(p, store), http.MethodPost, checkOffPath(), `{}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rec.Code)
	}
}

// TestCheckOff_400OnBadItemUUID pins that a malformed {id} is rejected with 400
// before the store is consulted.
func TestCheckOff_400OnBadItemUUID(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	store := &fakeCheckOffs{item: listitems.ListItem{ID: validID}}

	rec := do(t, newCheckOffRouter(p, store), http.MethodPost,
		"/v1/lists/"+validListID+"/items/not-a-uuid/check-off", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 (malformed item id)", rec.Code)
	}
	if store.gotUserID != "" {
		t.Fatalf("store was called (gotUserID=%q); want the handler to reject before reaching the store", store.gotUserID)
	}
}

// TestCheckOff_400OnBadClientEventID pins that a non-UUID client_event_id in the
// body is rejected with 400 before the store (and Postgres' $6::uuid cast) is
// consulted — a malformed client value must not surface as a 500.
func TestCheckOff_400OnBadClientEventID(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	store := &fakeCheckOffs{item: listitems.ListItem{ID: validID}}

	rec := do(t, newCheckOffRouter(p, store), http.MethodPost, checkOffPath(),
		`{"client_event_id":"not-a-uuid"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 (malformed client_event_id)", rec.Code)
	}
	if store.gotUserID != "" {
		t.Fatalf("store was called (gotUserID=%q); want the handler to reject before reaching the store", store.gotUserID)
	}
}

// TestCheckOff_400OnBadListUUID pins the same UUID validation on the {listId}
// path param, matching the nested list-item handlers.
func TestCheckOff_400OnBadListUUID(t *testing.T) {
	hh := "h-1"
	p := &web.Principal{UserID: "u-1", HouseholdID: &hh}
	store := &fakeCheckOffs{item: listitems.ListItem{ID: validID}}

	rec := do(t, newCheckOffRouter(p, store), http.MethodPost,
		"/v1/lists/not-a-uuid/items/"+validID+"/check-off", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 (malformed list id)", rec.Code)
	}
}
