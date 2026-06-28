package web_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5/middleware"

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

func TestRecovererReturnsJSON500(t *testing.T) {
	h := web.Recoverer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not json: %v", err)
	}
	if body["error"] == "" {
		t.Fatalf("expected an error message, got %v", body)
	}
}

func TestRecovererLogsPanicWithRequestID(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	// middleware.RequestID populates the request ID the recoverer should log.
	h := middleware.RequestID(web.Recoverer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log output is not JSON: %v\n%s", err, buf.String())
	}
	if rec["level"] != "ERROR" {
		t.Errorf("level = %v, want ERROR", rec["level"])
	}
	if id, ok := rec["request_id"].(string); !ok || id == "" {
		t.Errorf("request_id = %v, want a non-empty string", rec["request_id"])
	}
	if rec["panic"] != "boom" {
		t.Errorf("panic = %v, want boom", rec["panic"])
	}
}

func TestServerErrorLogsAndWritesGeneric500(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	// middleware.RequestID populates the request ID ServerError should log.
	h := middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		web.ServerError(w, r, errors.New("db boom"), "suggest failed")
	}))
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/", nil))

	// Client body stays generic: the configured message, no internal error.
	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not json: %v", err)
	}
	if body["error"] != "suggest failed" {
		t.Fatalf("client error = %q, want %q", body["error"], "suggest failed")
	}
	if got := resp.Body.String(); contains(got, "db boom") {
		t.Fatalf("internal error leaked to client: %s", got)
	}

	// Log record carries the underlying error and the request ID.
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log output is not JSON: %v\n%s", err, buf.String())
	}
	if rec["level"] != "ERROR" {
		t.Errorf("level = %v, want ERROR", rec["level"])
	}
	if rec["msg"] != "suggest failed" {
		t.Errorf("msg = %v, want %q", rec["msg"], "suggest failed")
	}
	if rec["err"] != "db boom" {
		t.Errorf("err = %v, want %q", rec["err"], "db boom")
	}
	if id, ok := rec["request_id"].(string); !ok || id == "" {
		t.Errorf("request_id = %v, want a non-empty string", rec["request_id"])
	}
}

func contains(s, sub string) bool {
	return bytes.Contains([]byte(s), []byte(sub))
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
