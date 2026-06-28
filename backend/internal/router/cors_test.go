package router_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stroem/shopping-list/backend/internal/router"
)

// TestCORS_CrossOriginGET asserts an allowed cross-origin GET is echoed back in
// the Access-Control-Allow-Origin header.
func TestCORS_CrossOriginGET(t *testing.T) {
	const origin = "https://app.example.com"
	h := router.New(router.Deps{
		DB:                 fakePinger{},
		CORSAllowedOrigins: []string{origin},
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", origin)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, origin)
	}
}

// TestCORS_Preflight asserts a preflight OPTIONS short-circuits with a 2xx (not
// the 405 MethodNotAllowed) and advertises our methods and headers.
func TestCORS_Preflight(t *testing.T) {
	const origin = "https://app.example.com"
	h := router.New(router.Deps{
		DB:                 fakePinger{},
		CORSAllowedOrigins: []string{origin},
	})

	// go-chi/cors reflects the *requested* method/headers in the preflight
	// response, and only echoes them when they fall within our configured
	// allow-list. So requesting DELETE plus the app's custom headers and seeing
	// them echoed back proves our config permits them.
	req := httptest.NewRequest(http.MethodOptions, "/v1/suggest", nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", http.MethodDelete)
	req.Header.Set("Access-Control-Request-Headers", "X-Device-Id, Content-Type, Idempotency-Key")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code < 200 || rec.Code >= 300 {
		t.Fatalf("preflight status = %d, want 2xx (not 405)", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, origin)
	}

	if methods := rec.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(methods, http.MethodDelete) {
		t.Errorf("Access-Control-Allow-Methods = %q, want it to permit DELETE", methods)
	}

	allowHeaders := strings.ToLower(rec.Header().Get("Access-Control-Allow-Headers"))
	for _, hdr := range []string{"x-device-id", "content-type", "idempotency-key"} {
		if !strings.Contains(allowHeaders, hdr) {
			t.Errorf("Access-Control-Allow-Headers = %q, missing %s", allowHeaders, hdr)
		}
	}
}

// TestCORS_DefaultAllowsLocalhost asserts the zero-value default keeps local dev
// (flutter run -d chrome on a random localhost port) working.
func TestCORS_DefaultAllowsLocalhost(t *testing.T) {
	const origin = "http://localhost:53219"
	h := router.New(router.Deps{DB: fakePinger{}})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", origin)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q (localhost default)", got, origin)
	}
}

// TestCORS_DisallowedOriginNoHeader asserts an origin outside the allow-list is
// not echoed, so the browser blocks it.
func TestCORS_DisallowedOriginNoHeader(t *testing.T) {
	h := router.New(router.Deps{
		DB:                 fakePinger{},
		CORSAllowedOrigins: []string{"https://app.example.com"},
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want empty for disallowed origin", got)
	}
}
