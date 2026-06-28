package router

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/stroem/shopping-list/backend/internal/households"
	"github.com/stroem/shopping-list/backend/internal/web"
)

// HouseholdStore is the household persistence the handlers need; *households.Store
// satisfies it, tests pass a fake.
type HouseholdStore interface {
	Create(ctx context.Context, userID string, name *string) (households.Household, error)
	JoinByCode(ctx context.Context, userID, code string) (households.Household, error)
	Get(ctx context.Context, id string) (households.Household, error)
}

func createHousehold(store HouseholdStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, _ := web.PrincipalFrom(r.Context()) // RequireAuth guarantees presence
		var body struct {
			Name *string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		h, err := store.Create(r.Context(), p.UserID, body.Name)
		if err != nil {
			web.Error(w, http.StatusInternalServerError, "create failed")
			return
		}
		web.JSON(w, http.StatusCreated, h)
	}
}

func joinHousehold(store HouseholdStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, _ := web.PrincipalFrom(r.Context())
		var body struct {
			InviteCode string `json:"invite_code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.InviteCode == "" {
			web.Error(w, http.StatusBadRequest, "invite_code required")
			return
		}
		h, err := store.JoinByCode(r.Context(), p.UserID, body.InviteCode)
		if errors.Is(err, households.ErrNotFound) {
			web.Error(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			web.Error(w, http.StatusInternalServerError, "join failed")
			return
		}
		web.JSON(w, http.StatusOK, h)
	}
}

func getHousehold(store HouseholdStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		mine, ok := web.HouseholdID(r.Context())
		// Not yours (or you have none) → 404, no existence leak.
		if !ok || mine != id {
			web.Error(w, http.StatusNotFound, "not found")
			return
		}
		h, err := store.Get(r.Context(), id)
		if errors.Is(err, households.ErrNotFound) {
			web.Error(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			web.Error(w, http.StatusInternalServerError, "get failed")
			return
		}
		web.JSON(w, http.StatusOK, h)
	}
}
