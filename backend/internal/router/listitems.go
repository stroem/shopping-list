package router

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/stroem/shopping-list/backend/internal/listitems"
	"github.com/stroem/shopping-list/backend/internal/web"
)

// ListItemStore is the list-item persistence the handlers need; *listitems.Store
// satisfies it, tests pass a fake.
type ListItemStore interface {
	Add(ctx context.Context, householdID, listID, id string, in listitems.AddInput) (listitems.ListItem, bool, error)
	List(ctx context.Context, householdID, listID string) ([]listitems.ListItem, error)
	Update(ctx context.Context, householdID, id string, quantity *int, note *string, position *int) (listitems.ListItem, error)
	SoftDelete(ctx context.Context, householdID, id string) error
}

// listItemIDs validates the {listId} and {id} path params as UUIDs, writing 400
// and returning ok=false when either is malformed.
func listItemIDs(w http.ResponseWriter, r *http.Request) (listID, id string, ok bool) {
	listID = chi.URLParam(r, "listId")
	if _, err := uuid.Parse(listID); err != nil {
		web.Error(w, http.StatusBadRequest, "invalid list id")
		return "", "", false
	}
	id = chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		web.Error(w, http.StatusBadRequest, "invalid list item id")
		return "", "", false
	}
	return listID, id, true
}

func putListItem(store ListItemStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hh, ok := web.HouseholdID(r.Context())
		if !ok {
			web.Error(w, http.StatusNotFound, "not found")
			return
		}
		listID, id, ok := listItemIDs(w, r)
		if !ok {
			return
		}
		var body struct {
			Name     string  `json:"name"`
			Quantity *int    `json:"quantity"`
			Note     *string `json:"note"`
			ItemID   *string `json:"item_id"`
			Aisle    *int    `json:"aisle"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
			web.Error(w, http.StatusBadRequest, "name required")
			return
		}
		if body.Quantity != nil && *body.Quantity < 0 {
			web.Error(w, http.StatusBadRequest, "quantity must not be negative")
			return
		}
		// A nil quantity passes 0 so the store defaults it to 1.
		quantity := 0
		if body.Quantity != nil {
			quantity = *body.Quantity
		}
		in := listitems.AddInput{
			Name:     body.Name,
			Quantity: quantity,
			Note:     body.Note,
			ItemID:   body.ItemID,
			Aisle:    body.Aisle,
		}
		li, created, err := store.Add(r.Context(), hh, listID, id, in)
		if errors.Is(err, listitems.ErrNotFound) {
			web.Error(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			web.ServerError(w, r, err, "add list item failed")
			return
		}
		status := http.StatusOK
		if created {
			status = http.StatusCreated
		}
		web.JSON(w, status, li)
	}
}

func listListItems(store ListItemStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hh, ok := web.HouseholdID(r.Context())
		if !ok {
			web.JSON(w, http.StatusOK, []listitems.ListItem{})
			return
		}
		listID := chi.URLParam(r, "listId")
		items, err := store.List(r.Context(), hh, listID)
		if err != nil {
			web.ServerError(w, r, err, "list list items failed")
			return
		}
		if items == nil {
			items = []listitems.ListItem{}
		}
		web.JSON(w, http.StatusOK, items)
	}
}

func patchListItem(store ListItemStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hh, ok := web.HouseholdID(r.Context())
		if !ok {
			web.Error(w, http.StatusNotFound, "not found")
			return
		}
		_, id, ok := listItemIDs(w, r)
		if !ok {
			return
		}
		var body struct {
			Quantity *int    `json:"quantity"`
			Note     *string `json:"note"`
			Position *int    `json:"position"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
			(body.Quantity == nil && body.Note == nil && body.Position == nil) {
			web.Error(w, http.StatusBadRequest, "quantity, note or position required")
			return
		}
		li, err := store.Update(r.Context(), hh, id, body.Quantity, body.Note, body.Position)
		if errors.Is(err, listitems.ErrNotFound) {
			web.Error(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			web.ServerError(w, r, err, "update list item failed")
			return
		}
		web.JSON(w, http.StatusOK, li)
	}
}

func deleteListItem(store ListItemStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hh, ok := web.HouseholdID(r.Context())
		if !ok {
			web.Error(w, http.StatusNotFound, "not found")
			return
		}
		_, id, ok := listItemIDs(w, r)
		if !ok {
			return
		}
		err := store.SoftDelete(r.Context(), hh, id)
		if errors.Is(err, listitems.ErrNotFound) {
			web.Error(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			web.ServerError(w, r, err, "delete list item failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
