package router

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/stroem/shopping-list/backend/internal/web"
)

// healthz reports 200 when the database answers a ping within the timeout,
// 503 otherwise. The server stays up either way.
func healthz(db Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			// Log the real ping error (with the request ID) for observability,
			// but keep the client body generic so we never leak DB internals.
			slog.ErrorContext(r.Context(), "healthz db ping failed",
				"err", err,
				"request_id", middleware.GetReqID(r.Context()),
			)
			web.JSON(w, http.StatusServiceUnavailable, map[string]string{
				"status": "unavailable",
				"error":  "database unavailable",
			})
			return
		}
		web.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}
