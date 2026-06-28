package router_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stroem/shopping-list/backend/internal/router"
)

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

func TestHealthzOK(t *testing.T) {
	h := router.New(router.Deps{DB: fakePinger{err: nil}})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["status"] != "ok" {
		t.Fatalf("status field = %v, want ok", body["status"])
	}
}

func TestHealthzDBDown(t *testing.T) {
	h := router.New(router.Deps{DB: fakePinger{err: errors.New("down")}})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["status"] != "unavailable" || body["error"] == nil {
		t.Fatalf("body = %v, want status=unavailable + error", body)
	}
}

func TestHealthzDBDown_LogsErrorAndDoesNotLeak(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	h := router.New(router.Deps{DB: fakePinger{err: errors.New("dial tcp: connection refused")}})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	// Raw error must not leak to the client body.
	if body := rec.Body.String(); bytes.Contains([]byte(body), []byte("connection refused")) {
		t.Fatalf("internal error leaked to client: %s", body)
	}

	var log map[string]any
	if err := json.Unmarshal(buf.Bytes(), &log); err != nil {
		t.Fatalf("log output is not JSON: %v\n%s", err, buf.String())
	}
	if log["err"] != "dial tcp: connection refused" {
		t.Errorf("logged err = %v, want the underlying ping error", log["err"])
	}
	if id, ok := log["request_id"].(string); !ok || id == "" {
		t.Errorf("request_id = %v, want a non-empty string", log["request_id"])
	}
}
