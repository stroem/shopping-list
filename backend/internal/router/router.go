// Package router builds the HTTP routing shared by the local server and the
// Lambda entrypoint, so the two can never diverge.
package router

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/stroem/shopping-list/backend/internal/auth"
	"github.com/stroem/shopping-list/backend/internal/web"
)

// Pinger is the database dependency the health check needs. *pgxpool.Pool
// satisfies it; tests pass a fake.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Deps are the runtime dependencies the router wires into handlers.
type Deps struct {
	DB             Pinger
	Suggest        Suggester
	AuthMiddleware func(http.Handler) http.Handler
	Households     HouseholdStore
}

// New returns the application's HTTP handler.
func New(deps Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(web.Recoverer)
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
		r.Get("/suggest", suggestHandler(deps.Suggest))
		if deps.Households != nil {
			r.Group(func(r chi.Router) {
				r.Use(auth.RequireAuth)
				r.Post("/households", createHousehold(deps.Households))
				r.Post("/households/join", joinHousehold(deps.Households))
				r.Get("/households/{id}", getHousehold(deps.Households))
			})
		}
	})
	return r
}
