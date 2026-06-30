package router

import (
	"context"
	"net/http"
	"time"

	syncpkg "github.com/stroem/shopping-list/backend/internal/sync"
	"github.com/stroem/shopping-list/backend/internal/web"
)

// SyncStore is the pull-sync read dependency; *sync.Store satisfies it.
type SyncStore interface {
	Changes(ctx context.Context, householdID string, since time.Time) (syncpkg.Result, error)
}

type syncResponse struct {
	Cursor  string                      `json:"cursor"`
	Changes map[string][]map[string]any `json:"changes"`
}

func syncHandler(store SyncStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		household, ok := web.HouseholdID(r.Context())
		if !ok {
			// Authenticated but not in a household yet: nothing to sync.
			web.JSON(w, http.StatusOK, syncResponse{Cursor: "", Changes: map[string][]map[string]any{}})
			return
		}
		var since time.Time
		if raw := r.URL.Query().Get("since"); raw != "" {
			t, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				web.Error(w, http.StatusBadRequest, "invalid since cursor")
				return
			}
			since = t
		}
		res, err := store.Changes(r.Context(), household, since)
		if err != nil {
			web.ServerError(w, r, err, "sync failed")
			return
		}
		cursor := ""
		if !res.Cursor.IsZero() {
			cursor = res.Cursor.UTC().Format(time.RFC3339Nano)
		}
		web.JSON(w, http.StatusOK, syncResponse{Cursor: cursor, Changes: res.Changes})
	}
}
