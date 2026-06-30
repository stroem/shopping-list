package idempotency

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stroem/shopping-list/backend/internal/web"
)

type fakeStore struct {
	saved   map[string]Response
	lookups int
	saves   int
}

func newFake() *fakeStore { return &fakeStore{saved: map[string]Response{}} }

func (f *fakeStore) Lookup(_ context.Context, household, key string) (*Response, bool, error) {
	f.lookups++
	r, ok := f.saved[household+"|"+key]
	if !ok {
		return nil, false, nil
	}
	return &r, true, nil
}

func (f *fakeStore) Save(_ context.Context, household, key, method, path string, status int, body []byte) error {
	f.saves++
	f.saved[household+"|"+key] = Response{StatusCode: status, Body: append([]byte(nil), body...)}
	return nil
}

// withHousehold puts a principal with a household into the request context.
func withHousehold(r *http.Request, hh string) *http.Request {
	return r.WithContext(web.WithPrincipal(r.Context(), web.Principal{UserID: "u", HouseholdID: &hh}))
}

func TestPassthroughWhenNotMutating(t *testing.T) {
	f := newFake()
	calls := 0
	h := Middleware(f)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; w.WriteHeader(200) }))
	req := withHousehold(httptest.NewRequest(http.MethodGet, "/v1/sync", nil), "hh")
	req.Header.Set("Idempotency-Key", "k1")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if calls != 1 || f.lookups != 0 {
		t.Fatalf("GET should pass through untouched: calls=%d lookups=%d", calls, f.lookups)
	}
}

func TestPassthroughWhenNoKey(t *testing.T) {
	f := newFake()
	calls := 0
	h := Middleware(f)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; w.WriteHeader(201) }))
	req := withHousehold(httptest.NewRequest(http.MethodPost, "/v1/x", nil), "hh")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if calls != 1 || f.saves != 0 {
		t.Fatalf("no key should pass through without save: calls=%d saves=%d", calls, f.saves)
	}
}

func TestFirstCallRunsAndSaves(t *testing.T) {
	f := newFake()
	calls := 0
	h := Middleware(f)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		web.JSON(w, http.StatusCreated, map[string]string{"id": "abc"})
	}))
	req := withHousehold(httptest.NewRequest(http.MethodPost, "/v1/x", nil), "hh")
	req.Header.Set("Idempotency-Key", "k1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if calls != 1 || f.saves != 1 {
		t.Fatalf("first call: calls=%d saves=%d, want 1/1", calls, f.saves)
	}
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), "abc") {
		t.Fatalf("first response: %d %q", rec.Code, rec.Body.String())
	}
}

func TestReplayReturnsStoredAndSkipsHandler(t *testing.T) {
	f := newFake()
	calls := 0
	h := Middleware(f)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		web.JSON(w, http.StatusCreated, map[string]int{"n": calls})
	}))
	mk := func() *http.Request {
		req := withHousehold(httptest.NewRequest(http.MethodPost, "/v1/x", nil), "hh")
		req.Header.Set("Idempotency-Key", "k1")
		return req
	}
	first := httptest.NewRecorder()
	h.ServeHTTP(first, mk())
	second := httptest.NewRecorder()
	h.ServeHTTP(second, mk())
	if calls != 1 {
		t.Fatalf("handler ran %d times, want 1 (replay must skip handler)", calls)
	}
	if first.Body.String() != second.Body.String() || second.Code != first.Code {
		t.Fatalf("replay mismatch: %d %q vs %d %q", first.Code, first.Body.String(), second.Code, second.Body.String())
	}
}

func TestDifferentKeyRunsAgain(t *testing.T) {
	f := newFake()
	calls := 0
	h := Middleware(f)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; w.WriteHeader(201) }))
	for _, k := range []string{"k1", "k2"} {
		req := withHousehold(httptest.NewRequest(http.MethodPost, "/v1/x", nil), "hh")
		req.Header.Set("Idempotency-Key", k)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	if calls != 2 {
		t.Fatalf("distinct keys: calls=%d, want 2", calls)
	}
}

func TestServerErrorNotStored(t *testing.T) {
	f := newFake()
	h := Middleware(f)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		web.Error(w, http.StatusInternalServerError, "boom")
	}))
	req := withHousehold(httptest.NewRequest(http.MethodPost, "/v1/x", nil), "hh")
	req.Header.Set("Idempotency-Key", "k1")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if f.saves != 0 {
		t.Fatalf("5xx must not be stored: saves=%d", f.saves)
	}
}

var _ io.Writer = (*recorder)(nil) // recorder is an http.ResponseWriter wrapper
