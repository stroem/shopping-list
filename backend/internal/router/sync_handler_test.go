package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	syncpkg "github.com/stroem/shopping-list/backend/internal/sync"
	"github.com/stroem/shopping-list/backend/internal/web"
)

type fakeSync struct {
	res  syncpkg.Result
	got  string
	gotS time.Time
}

func (f *fakeSync) Changes(_ context.Context, household string, since time.Time) (syncpkg.Result, error) {
	f.got = household
	f.gotS = since
	return f.res, nil
}

func reqWithHousehold(target, hh string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	return r.WithContext(web.WithPrincipal(r.Context(), web.Principal{UserID: "u", HouseholdID: &hh}))
}

func TestSyncHandlerOK(t *testing.T) {
	cur := time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC)
	f := &fakeSync{res: syncpkg.Result{Cursor: cur, Changes: map[string][]map[string]any{"lists": {{"id": "x"}}}}}
	rec := httptest.NewRecorder()
	syncHandler(f)(rec, reqWithHousehold("/v1/sync?since=2026-06-01T00:00:00Z", "hh1"))
	if rec.Code != 200 {
		t.Fatalf("code=%d", rec.Code)
	}
	var body syncResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body.Cursor != "2026-06-30T10:00:00Z" {
		t.Fatalf("cursor=%q", body.Cursor)
	}
	if len(body.Changes["lists"]) != 1 {
		t.Fatalf("changes=%v", body.Changes)
	}
	if f.got != "hh1" || f.gotS.IsZero() {
		t.Fatalf("store got household=%q since=%v", f.got, f.gotS)
	}
}

func TestSyncHandlerBadSince(t *testing.T) {
	rec := httptest.NewRecorder()
	syncHandler(&fakeSync{})(rec, reqWithHousehold("/v1/sync?since=not-a-time", "hh1"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d, want 400", rec.Code)
	}
}

func TestSyncHandlerNoHousehold(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/v1/sync", nil)
	r = r.WithContext(web.WithPrincipal(r.Context(), web.Principal{UserID: "u"})) // no household
	rec := httptest.NewRecorder()
	syncHandler(&fakeSync{})(rec, r)
	if rec.Code != 200 {
		t.Fatalf("code=%d, want 200 empty", rec.Code)
	}
}
