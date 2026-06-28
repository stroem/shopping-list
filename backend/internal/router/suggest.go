package router

import (
	"context"
	"net/http"
	"strconv"

	"github.com/stroem/shopping-list/backend/internal/suggest"
	"github.com/stroem/shopping-list/backend/internal/web"
)

// Suggester is the autocomplete dependency the suggest handler needs.
// *suggest.Service satisfies it; tests pass a fake.
type Suggester interface {
	Suggest(ctx context.Context, deviceID, q string, limit int) ([]suggest.Suggestion, error)
}

// suggestHandler answers GET /v1/suggest?q=&limit=, scoped to the caller's
// household (resolved by the service from X-Device-Id).
func suggestHandler(s Suggester) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit")) // 0 on absent/bad → service clamps
		deviceID := web.DeviceID(r.Context())

		results, err := s.Suggest(r.Context(), deviceID, q, limit)
		if err != nil {
			web.ServerError(w, r, err, "suggest failed")
			return
		}
		if results == nil {
			results = []suggest.Suggestion{} // marshal to [] never null
		}
		web.JSON(w, http.StatusOK, results)
	}
}
