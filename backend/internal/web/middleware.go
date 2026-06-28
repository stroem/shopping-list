package web

import (
	"context"
	"net/http"
)

type ctxKey int

const deviceIDKey ctxKey = iota

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
