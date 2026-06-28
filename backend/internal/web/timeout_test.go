package web_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stroem/shopping-list/backend/internal/web"
)

// TestTimeoutWritesJSON504 confirms that a handler which outlives the deadline
// yields a single JSON 504 with the {"error":...} shape, and does not hang.
func TestTimeoutWritesJSON504(t *testing.T) {
	// A handler that blocks until its request context is cancelled, then tries
	// (and must fail) to write a 200 — exercising the write-once guard.
	blocking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	h := web.Timeout(10 * time.Millisecond)(blocking)

	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler hung past the deadline")
	}

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, rec.Body.String())
	}
	if body["error"] == "" {
		t.Fatalf("body = %v, want non-empty error field", body)
	}
	if _, ok := body["status"]; ok {
		t.Fatalf("late handler write leaked into response: %v", body)
	}
}

// TestTimeoutPassesThroughFastHandler confirms a handler that finishes before
// the deadline has its status and body delivered untouched.
func TestTimeoutPassesThroughFastHandler(t *testing.T) {
	fast := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		web.JSON(w, http.StatusCreated, map[string]string{"hello": "world"})
	})
	h := web.Timeout(time.Second)(fast)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, rec.Body.String())
	}
	if body["hello"] != "world" {
		t.Fatalf("body = %v, want hello=world", body)
	}
}

// TestTimeoutSetsDeadlineOnContext confirms the middleware bounds the request
// context so downstream DB queries observe a deadline.
func TestTimeoutSetsDeadlineOnContext(t *testing.T) {
	var hasDeadline bool
	h := web.Timeout(time.Second)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, hasDeadline = r.Context().Deadline()
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !hasDeadline {
		t.Fatal("request context has no deadline")
	}
}
