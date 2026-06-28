package web

import (
	"context"
	"log"
	"net/http"
)

type ctxKey int

const deviceIDKey ctxKey = iota

// Recoverer converts a panic in a downstream handler into a JSON 500 response,
// so error bodies stay uniform with the rest of the API. http.ErrAbortHandler is
// re-panicked, matching net/http's convention for intentional aborts.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				log.Printf("panic recovered: %v", rec)
				Error(w, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// DeviceIDMiddleware copies the X-Device-Id request header into the context.
// It does not enforce presence — auth lands with the households work.
func DeviceIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Device-Id")
		ctx := context.WithValue(r.Context(), deviceIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// DeviceID returns the X-Device-Id captured by DeviceIDMiddleware, or "".
func DeviceID(ctx context.Context) string {
	id, _ := ctx.Value(deviceIDKey).(string)
	return id
}
