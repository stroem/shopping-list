package router_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stroem/shopping-list/backend/internal/router"
)

// newLimitedRouter builds a router whose suggest endpoint allows `limit`
// requests per a deliberately long window, so a burst lands in a single window
// and the test stays deterministic.
func newLimitedRouter(limit int) http.Handler {
	return router.New(router.Deps{
		DB:                fakePinger{},
		Suggest:           &fakeSuggester{},
		SuggestRateLimit:  limit,
		SuggestRateWindow: time.Hour,
	})
}

// TestSuggestRateLimit_BurstReturns429 fires limit+1 requests with the same
// device id; requests up to the limit succeed and the over-limit one is a JSON
// 429.
func TestSuggestRateLimit_BurstReturns429(t *testing.T) {
	const limit = 2
	h := newLimitedRouter(limit)

	for i := 0; i < limit; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/suggest?q=x", nil)
		req.Header.Set("X-Device-Id", "dev-burst")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200 (under limit)", i+1, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/suggest?q=x", nil)
	req.Header.Set("X-Device-Id", "dev-burst")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("over-limit status = %d, want 429", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("429 body is not JSON: %v (%s)", err, rec.Body.String())
	}
	if body["error"] == nil {
		t.Fatalf("429 body = %s, want {\"error\": ...}", rec.Body.String())
	}
}

// TestSuggestRateLimit_PerDeviceKeyed asserts the limiter is keyed per device:
// one device exhausting its budget does not block another.
func TestSuggestRateLimit_PerDeviceKeyed(t *testing.T) {
	const limit = 1
	h := newLimitedRouter(limit)

	for _, dev := range []string{"dev-a", "dev-b"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/suggest?q=x", nil)
		req.Header.Set("X-Device-Id", dev)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("device %s first request: status = %d, want 200", dev, rec.Code)
		}
	}
}

// TestHealthzNotRateLimited hammers /healthz well past the suggest limit; it
// must never return 429 because liveness checks are exempt.
func TestHealthzNotRateLimited(t *testing.T) {
	const limit = 2
	h := newLimitedRouter(limit)

	for i := 0; i < limit*3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.Header.Set("X-Device-Id", "dev-health")
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("healthz request %d returned 429, want it exempt from rate limiting", i+1)
		}
	}
}

// TestSuggestRateLimit_ZeroDepsNoLimit asserts a zero-value limit (as in plain
// Deps{} tests) falls back to a sane default and does not 429 a small burst.
func TestSuggestRateLimit_ZeroDepsNoLimit(t *testing.T) {
	h := router.New(router.Deps{DB: fakePinger{}, Suggest: &fakeSuggester{}})

	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/suggest?q=x", nil)
		req.Header.Set("X-Device-Id", "dev-default")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200 with default limit", i+1, rec.Code)
		}
	}
}
