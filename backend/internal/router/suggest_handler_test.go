package router_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stroem/shopping-list/backend/internal/router"
	"github.com/stroem/shopping-list/backend/internal/suggest"
)

type fakeSuggester struct {
	gotDevice, gotQ string
	gotLimit        int
	out             []suggest.Suggestion
	err             error
}

func (f *fakeSuggester) Suggest(_ context.Context, deviceID, q string, limit int) ([]suggest.Suggestion, error) {
	f.gotDevice, f.gotQ, f.gotLimit = deviceID, q, limit
	return f.out, f.err
}

func doSuggest(t *testing.T, fake *fakeSuggester, target, deviceID string) *httptest.ResponseRecorder {
	t.Helper()
	h := router.New(router.Deps{DB: fakePinger{}, Suggest: fake})
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if deviceID != "" {
		req.Header.Set("X-Device-Id", deviceID)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSuggestHandler_PassesParamsAndReturnsArray(t *testing.T) {
	aisle := 2
	fake := &fakeSuggester{out: []suggest.Suggestion{{Name: "Mjölk 3%", Aisle: &aisle, Source: "food_catalog"}}}
	rec := doSuggest(t, fake, "/v1/suggest?q=mj&limit=5", "dev-9")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if fake.gotDevice != "dev-9" || fake.gotQ != "mj" || fake.gotLimit != 5 {
		t.Fatalf("passthrough = %q/%q/%d, want dev-9/mj/5", fake.gotDevice, fake.gotQ, fake.gotLimit)
	}
	var got []suggest.Suggestion
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body not a JSON array: %v (%s)", err, rec.Body.String())
	}
	if len(got) != 1 || got[0].Name != "Mjölk 3%" {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestSuggestHandler_EmptyResultIsArrayNotNull(t *testing.T) {
	rec := doSuggest(t, &fakeSuggester{out: nil}, "/v1/suggest?q=zzz", "")
	if body := rec.Body.String(); body != "[]\n" {
		t.Fatalf("empty body = %q, want %q", body, "[]\n")
	}
}

func TestSuggestHandler_BadLimitDefaultsToZero(t *testing.T) {
	fake := &fakeSuggester{}
	doSuggest(t, fake, "/v1/suggest?q=mj&limit=abc", "")
	if fake.gotLimit != 0 { // service clamps 0 → default
		t.Fatalf("limit = %d, want 0 (service clamps)", fake.gotLimit)
	}
}

func TestSuggestHandler_ServiceErrorIs500(t *testing.T) {
	rec := doSuggest(t, &fakeSuggester{err: errors.New("boom")}, "/v1/suggest?q=mj", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}
