package router

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/google/uuid"
	"github.com/stroem/shopping-list/backend/internal/listitems"
	"github.com/stroem/shopping-list/backend/internal/web"
)

// CheckOffStore is the check-off persistence the handler needs; *checkoffs.Store
// satisfies it, tests pass a fake. Kept to primitives + listitems.ListItem so the
// router package never imports checkoffs.
type CheckOffStore interface {
	CheckOff(ctx context.Context, householdID, id, userID string, clientEventID *string) (listitems.ListItem, error)
}

// checkOffListItem ticks off a list item, recording an append-only check-off
// event for the authenticated caller and returning the updated list item.
func checkOffListItem(store CheckOffStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := web.PrincipalFrom(r.Context())
		hh, hasHousehold := web.HouseholdID(r.Context())
		if !ok || !hasHousehold {
			web.Error(w, http.StatusNotFound, "not found")
			return
		}
		_, id, ok := listItemIDs(w, r)
		if !ok {
			return
		}
		var body struct {
			ClientEventID *string `json:"client_event_id"`
		}
		// An empty body is valid: client_event_id stays nil.
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			web.Error(w, http.StatusBadRequest, "invalid body")
			return
		}
		clientEventID := body.ClientEventID
		if clientEventID != nil && *clientEventID == "" {
			clientEventID = nil
		}
		if clientEventID != nil {
			if _, err := uuid.Parse(*clientEventID); err != nil {
				web.Error(w, http.StatusBadRequest, "invalid client_event_id")
				return
			}
		}
		li, err := store.CheckOff(r.Context(), hh, id, p.UserID, clientEventID)
		if errors.Is(err, listitems.ErrNotFound) {
			web.Error(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			web.ServerError(w, r, err, "check off list item failed")
			return
		}
		web.JSON(w, http.StatusOK, li)
	}
}
