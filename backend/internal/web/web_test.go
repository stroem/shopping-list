package web_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stroem/shopping-list/backend/internal/web"
)

func TestErrorWritesJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	web.Error(rec, http.StatusTeapot, "nope")

	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not json: %v", err)
	}
	if body["error"] != "nope" {
		t.Fatalf("error = %q, want nope", body["error"])
	}
}

func TestDeviceIDMiddlewarePopulatesContext(t *testing.T) {
	var got string
	h := web.DeviceIDMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = web.DeviceID(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Device-Id", "dev-123")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got != "dev-123" {
		t.Fatalf("device id = %q, want dev-123", got)
	}
}
