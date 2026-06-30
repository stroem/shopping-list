package router

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/stroem/shopping-list/backend/internal/lists"
	"github.com/stroem/shopping-list/backend/internal/web"
)

// ListStore is the list persistence the handlers need; *lists.Store satisfies
// it, tests pass a fake.
type ListStore interface {
	Upsert(ctx context.Context, householdID, id, name string) (lists.List, bool, error)
	List(ctx context.Context, householdID string) ([]lists.List, error)
	Get(ctx context.Context, householdID, id string) (lists.List, error)
	Update(ctx context.Context, householdID, id string, name *string, archived *bool) (lists.List, error)
	SoftDelete(ctx context.Context, householdID, id string) error
}

// listID validates the {id} path param as a UUID, writing 400 and returning
// ok=false when it is malformed.
func listID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		web.Error(w, http.StatusBadRequest, "invalid list id")
		return "", false
	}
	return id, true
}

func putList(store ListStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hh, ok := web.HouseholdID(r.Context())
		if !ok {
			web.Error(w, http.StatusNotFound, "not found")
			return
		}
		id, ok := listID(w, r)
		if !ok {
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
			web.Error(w, http.StatusBadRequest, "name required")
			return
		}
		l, created, err := store.Upsert(r.Context(), hh, id, body.Name)
		if errors.Is(err, lists.ErrNotFound) {
			web.Error(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			web.ServerError(w, r, err, "create list failed")
			return
		}
		status := http.StatusOK
		if created {
			status = http.StatusCreated
		}
		web.JSON(w, status, l)
	}
}

func listLists(store ListStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hh, ok := web.HouseholdID(r.Context())
		if !ok {
			web.JSON(w, http.StatusOK, []lists.List{})
			return
		}
		ls, err := store.List(r.Context(), hh)
		if err != nil {
			web.ServerError(w, r, err, "list lists failed")
			return
		}
		if ls == nil {
			ls = []lists.List{}
		}
		web.JSON(w, http.StatusOK, ls)
	}
}

func getList(store ListStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hh, ok := web.HouseholdID(r.Context())
		if !ok {
			web.Error(w, http.StatusNotFound, "not found")
			return
		}
		id, ok := listID(w, r)
		if !ok {
			return
		}
		l, err := store.Get(r.Context(), hh, id)
		if errors.Is(err, lists.ErrNotFound) {
			web.Error(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			web.ServerError(w, r, err, "get list failed")
			return
		}
		web.JSON(w, http.StatusOK, l)
	}
}

func patchList(store ListStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hh, ok := web.HouseholdID(r.Context())
		if !ok {
			web.Error(w, http.StatusNotFound, "not found")
			return
		}
		id, ok := listID(w, r)
		if !ok {
			return
		}
		var body struct {
			Name     *string `json:"name"`
			Archived *bool   `json:"archived"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || (body.Name == nil && body.Archived == nil) {
			web.Error(w, http.StatusBadRequest, "name or archived required")
			return
		}
		if body.Name != nil && *body.Name == "" {
			web.Error(w, http.StatusBadRequest, "name must not be empty")
			return
		}
		l, err := store.Update(r.Context(), hh, id, body.Name, body.Archived)
		if errors.Is(err, lists.ErrNotFound) {
			web.Error(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			web.ServerError(w, r, err, "update list failed")
			return
		}
		web.JSON(w, http.StatusOK, l)
	}
}

func deleteList(store ListStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hh, ok := web.HouseholdID(r.Context())
		if !ok {
			web.Error(w, http.StatusNotFound, "not found")
			return
		}
		id, ok := listID(w, r)
		if !ok {
			return
		}
		err := store.SoftDelete(r.Context(), hh, id)
		if errors.Is(err, lists.ErrNotFound) {
			web.Error(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			web.ServerError(w, r, err, "delete list failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
