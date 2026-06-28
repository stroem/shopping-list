package router_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stroem/shopping-list/backend/internal/router"
	"github.com/stroem/shopping-list/backend/internal/suggest"
)

// blockingSuggester blocks until its request context is cancelled, simulating a
// slow/cold DB query that would otherwise hold a pooled connection forever.
type blockingSuggester struct{}

func (blockingSuggester) Suggest(ctx context.Context, _, _ string, _ int) ([]suggest.Suggestion, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestSuggestTimesOutWithJSON504 wires a tiny request timeout via Deps and a
// blocking suggester, asserting the response is a JSON 504 and never hangs.
func TestSuggestTimesOutWithJSON504(t *testing.T) {
	h := router.New(router.Deps{
		DB:             fakePinger{},
		Suggest:        blockingSuggester{},
		RequestTimeout: 10 * time.Millisecond,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/suggest?q=mj", nil)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		h.ServeHTTP(rec, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("request hung past the deadline")
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
}
