package router

import (
	"context"
	"net/http"
	"time"

	"github.com/stroem/shopping-list/backend/internal/web"
)

// healthz reports 200 when the database answers a ping within the timeout,
// 503 otherwise. The server stays up either way.
func healthz(db Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			web.JSON(w, http.StatusServiceUnavailable, map[string]string{
				"status": "unavailable",
				"error":  err.Error(),
			})
			return
		}
		web.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}
