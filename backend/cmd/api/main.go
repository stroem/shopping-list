// Command api runs the HTTP backend locally. It is the local-dev baseline and
// shares its router with the Lambda entrypoint. It requires DATABASE_URL.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/stroem/shopping-list/backend/internal/auth"
	"github.com/stroem/shopping-list/backend/internal/config"
	"github.com/stroem/shopping-list/backend/internal/db"
	"github.com/stroem/shopping-list/backend/internal/households"
	"github.com/stroem/shopping-list/backend/internal/router"
	"github.com/stroem/shopping-list/backend/internal/suggest"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pool.Close()

	verifier := buildVerifier(ctx, cfg)
	srv := &http.Server{
		Addr: ":" + cfg.Port,
		Handler: router.New(router.Deps{
			DB:             pool,
			Suggest:        suggest.New(pool),
			AuthMiddleware: auth.Middleware(verifier, auth.NewUserStore(pool)),
			Households:     households.NewStore(pool),
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("api listening on %s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
}

// buildVerifier returns an OIDC verifier when an audience is configured, else a
// deny verifier so the server still boots locally (auth endpoints return 401).
func buildVerifier(ctx context.Context, cfg config.Config) auth.TokenVerifier {
	if cfg.OIDCAudience == "" {
		log.Print("OIDC_AUDIENCE unset — auth disabled (auth endpoints return 401)")
		return auth.NewDenyVerifier()
	}
	v, err := auth.NewOIDCVerifier(ctx, cfg.OIDCIssuer, cfg.OIDCAudience)
	if err != nil {
		log.Fatalf("oidc: %v", err)
	}
	return v
}
