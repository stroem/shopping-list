// Package router builds the HTTP routing shared by the local server and the
// Lambda entrypoint, so the two can never diverge.
package router

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"github.com/stroem/shopping-list/backend/internal/auth"
	"github.com/stroem/shopping-list/backend/internal/web"
)

// defaultCORSOrigins keeps local web dev (flutter run -d chrome serves on a
// random localhost port) working out of the box when CORS_ALLOWED_ORIGINS is
// unset, without ever defaulting to an unconditional "*". Operators set the env
// var to their real web origin(s) in production.
var defaultCORSOrigins = []string{"http://localhost:*", "http://127.0.0.1:*"}

// Pinger is the database dependency the health check needs. *pgxpool.Pool
// satisfies it; tests pass a fake.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Deps are the runtime dependencies the router wires into handlers.
type Deps struct {
	DB      Pinger
	Suggest Suggester
	// RequestTimeout bounds every request; a non-positive value falls back to
	// web.DefaultRequestTimeout so zero-value Deps stay safe and testable.
	RequestTimeout time.Duration
	AuthMiddleware func(http.Handler) http.Handler
	Households     HouseholdStore
	Lists          ListStore
	// ListItems serves the household-scoped list-item routes; nil disables them.
	ListItems ListItemStore
	// CheckOffs serves the list-item check-off route; nil disables it.
	CheckOffs CheckOffStore
	// Sync serves GET /v1/sync; nil disables the route.
	Sync SyncStore
	// IdempotencyMiddleware wraps authenticated write routes so repeated
	// Idempotency-Key requests replay. nil disables it.
	IdempotencyMiddleware func(http.Handler) http.Handler
	// CORSAllowedOrigins are the cross-origin web origins allowed to call the API.
	// Empty falls back to defaultCORSOrigins (local dev only), never "*".
	CORSAllowedOrigins []string
	// SuggestRateLimit caps /v1/suggest requests per client per
	// SuggestRateWindow; a non-positive value falls back to a sane default so
	// zero-value Deps stay safe and testable.
	SuggestRateLimit int
	// SuggestRateWindow is the sliding window for SuggestRateLimit; non-positive
	// falls back to the default.
	SuggestRateWindow time.Duration
}

// New returns the application's HTTP handler.
func New(deps Deps) http.Handler {
	r := chi.NewRouter()

	allowedOrigins := deps.CORSAllowedOrigins
	if len(allowedOrigins) == 0 {
		allowedOrigins = defaultCORSOrigins
	}
	// CORS runs first so preflight OPTIONS short-circuits with a 2xx before chi
	// routing can return 405 MethodNotAllowed. AllowCredentials stays false: auth
	// is the X-Device-Id header / join code, not cookies.
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   allowedOrigins,
		AllowedMethods:   []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions},
		AllowedHeaders:   []string{"Accept", "Content-Type", "X-Device-Id", "Idempotency-Key", "Authorization"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	r.Use(middleware.RequestID)
	r.Use(web.Recoverer)
	r.Use(web.Timeout(deps.RequestTimeout))
	r.Use(web.DeviceIDMiddleware)
	if deps.AuthMiddleware != nil {
		r.Use(deps.AuthMiddleware)
	}

	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		web.Error(w, http.StatusNotFound, "not found")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		web.Error(w, http.StatusMethodNotAllowed, "method not allowed")
	})

	r.Get("/healthz", healthz(deps.DB))

	r.Route("/v1", func(r chi.Router) {
		// Rate-limit the public /v1 group per client. It runs after the global
		// DeviceIDMiddleware above, so web.DeviceID(ctx) is populated for keying.
		// healthz lives outside /v1 and is never limited (liveness must not 429).
		r.Use(suggestRateLimiter(deps.SuggestRateLimit, deps.SuggestRateWindow))
		r.Get("/suggest", suggestHandler(deps.Suggest))
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireAuth)
			if deps.IdempotencyMiddleware != nil {
				r.Use(deps.IdempotencyMiddleware)
			}
			if deps.Households != nil {
				r.Post("/households", createHousehold(deps.Households))
				r.Post("/households/join", joinHousehold(deps.Households))
				r.Get("/households/{id}", getHousehold(deps.Households))
			}
			if deps.Lists != nil {
				r.Put("/lists/{id}", putList(deps.Lists))
				r.Get("/lists", listLists(deps.Lists))
				r.Get("/lists/{id}", getList(deps.Lists))
				r.Patch("/lists/{id}", patchList(deps.Lists))
				r.Delete("/lists/{id}", deleteList(deps.Lists))
			}
			if deps.ListItems != nil {
				r.Put("/lists/{listId}/items/{id}", putListItem(deps.ListItems))
				r.Get("/lists/{listId}/items", listListItems(deps.ListItems))
				r.Patch("/lists/{listId}/items/{id}", patchListItem(deps.ListItems))
				r.Delete("/lists/{listId}/items/{id}", deleteListItem(deps.ListItems))
			}
			if deps.CheckOffs != nil {
				r.Post("/lists/{listId}/items/{id}/check-off", checkOffListItem(deps.CheckOffs))
			}
			if deps.Sync != nil {
				r.Get("/sync", syncHandler(deps.Sync))
			}
		})
	})
	return r
}
