// Package idempotency makes mutating requests safe to retry: a repeated
// (household, Idempotency-Key) replays the first response instead of re-running
// the handler, so double-taps and outbox replays never duplicate writes.
package idempotency

import (
	"bytes"
	"context"
	"net/http"

	"github.com/stroem/shopping-list/backend/internal/web"
)

// Response is a previously-stored idempotent response.
type Response struct {
	StatusCode int
	Body       []byte
}

// Store persists and retrieves idempotent responses keyed by (household, key).
type Store interface {
	Lookup(ctx context.Context, householdID, key string) (*Response, bool, error)
	Save(ctx context.Context, householdID, key, method, path string, status int, body []byte) error
}

func isMutating(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// Middleware replays a stored response for a repeated (household, Idempotency-Key)
// on mutating requests. Requests without the header, without a household
// principal, or with a non-mutating method pass straight through.
func Middleware(store Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get("Idempotency-Key")
			household, ok := web.HouseholdID(r.Context())
			if key == "" || !ok || !isMutating(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			if saved, found, err := store.Lookup(r.Context(), household, key); err == nil && found {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(saved.StatusCode)
				_, _ = w.Write(saved.Body)
				return
			}
			rec := &recorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			// Persist only deterministic outcomes; a 5xx may be transient, so the
			// client can retry it.
			if rec.status < 500 {
				_ = store.Save(r.Context(), household, key, r.Method, r.URL.Path, rec.status, rec.buf.Bytes())
			}
		})
	}
}

// recorder tees the handler's response to the client while capturing status+body
// so the first response can be persisted for replay.
type recorder struct {
	http.ResponseWriter
	status int
	buf    bytes.Buffer
	wrote  bool
}

func (rec *recorder) WriteHeader(code int) {
	if rec.wrote {
		return
	}
	rec.status = code
	rec.wrote = true
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *recorder) Write(b []byte) (int, error) {
	if !rec.wrote {
		rec.WriteHeader(http.StatusOK)
	}
	rec.buf.Write(b)
	return rec.ResponseWriter.Write(b)
}
