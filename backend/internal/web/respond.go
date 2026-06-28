// Package web holds HTTP helpers shared by every handler: JSON responses and
// request middleware. Domain packages build on these so responses stay uniform.
package web

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
)

// JSON writes v as a JSON body with the given status code.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Error writes {"error": msg} as JSON with the given status code.
func Error(w http.ResponseWriter, status int, msg string) {
	JSON(w, status, map[string]string{"error": msg})
}

// ServerError logs the underlying err (with the chi request ID, so the log line
// correlates to the response) and then writes a generic 500 body. The client
// never sees internal error details; logs keep them for observability. This is
// the standard way for a handler to map an internal failure to a 5xx.
func ServerError(w http.ResponseWriter, r *http.Request, err error, msg string) {
	slog.ErrorContext(r.Context(), msg,
		"err", err,
		"request_id", middleware.GetReqID(r.Context()),
	)
	Error(w, http.StatusInternalServerError, msg)
}
