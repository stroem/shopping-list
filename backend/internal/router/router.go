// Package router builds the HTTP routing shared by the local server and the
// Lambda entrypoint, so the two can never diverge.
package router

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// New returns the application's HTTP handler. It deliberately has no database
// dependency yet — a DB-backed health check arrives with the persistence layer.
func New() http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return r
}
