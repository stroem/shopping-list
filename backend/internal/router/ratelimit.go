package router

import (
	"net/http"
	"time"

	"github.com/go-chi/httprate"

	"github.com/stroem/shopping-list/backend/internal/web"
)

// Suggest rate-limit fallbacks so a zero-value Deps (used by existing tests and
// any caller that does not set the fields) still gets a sane limit rather than
// an open or closed endpoint.
const (
	defaultSuggestRateLimit  = 60
	defaultSuggestRateWindow = time.Minute
)

// suggestRateLimiter builds the per-client limiter for the suggest endpoint.
//
// Storage is in-process: httprate's default local counter lives in this
// process's memory. That is the right call here because the API runs as a
// single long-lived process locally and as a Lambda in production, and we keep
// running cost ≈ 0 — a shared store (Redis/DynamoDB) would add always-on cost
// and latency for a public, low-stakes autocomplete. The caveat: under Lambda,
// each warm instance counts independently, so the effective ceiling is
// limit × concurrent instances. That is acceptable as a coarse abuse guard
// protecting the tiny DB pool; it is not a precise global quota.
func suggestRateLimiter(limit int, window time.Duration) func(http.Handler) http.Handler {
	if limit <= 0 {
		limit = defaultSuggestRateLimit
	}
	if window <= 0 {
		window = defaultSuggestRateWindow
	}
	return httprate.Limit(
		limit,
		window,
		httprate.WithKeyFuncs(suggestRateKey),
		httprate.WithLimitHandler(func(w http.ResponseWriter, _ *http.Request) {
			web.Error(w, http.StatusTooManyRequests, "rate limit exceeded")
		}),
	)
}

// suggestRateKey keys the limiter per client: by X-Device-Id when present
// (populated by web.DeviceIDMiddleware, which runs before this limiter), else by
// client IP so header-less callers are still bounded.
func suggestRateKey(r *http.Request) (string, error) {
	if deviceID := web.DeviceID(r.Context()); deviceID != "" {
		return "dev:" + deviceID, nil
	}
	return httprate.KeyByIP(r)
}
