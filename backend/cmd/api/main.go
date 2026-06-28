// Command api runs the HTTP backend locally. It is the local-dev baseline and
// shares its router with the Lambda entrypoint. It requires DATABASE_URL.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/stroem/shopping-list/backend/internal/auth"
	"github.com/stroem/shopping-list/backend/internal/config"
	"github.com/stroem/shopping-list/backend/internal/db"
	"github.com/stroem/shopping-list/backend/internal/households"
	"github.com/stroem/shopping-list/backend/internal/logging"
	"github.com/stroem/shopping-list/backend/internal/router"
	"github.com/stroem/shopping-list/backend/internal/suggest"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		// Config failed before LOG_LEVEL is known; log at the default level.
		slog.SetDefault(logging.New(os.Stderr, "info"))
		slog.Error("config", "err", err)
		os.Exit(1)
	}

	logger := logging.New(os.Stderr, cfg.LogLevel)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	verifier := buildVerifier(ctx, cfg)
	srv := &http.Server{
		Addr: ":" + cfg.Port,
		Handler: router.New(router.Deps{
			DB:                 pool,
			Suggest:            suggest.New(pool),
			RequestTimeout:     cfg.RequestTimeout,
			AuthMiddleware:     auth.Middleware(verifier, auth.NewUserStore(pool)),
			Households:         households.NewStore(pool),
			CORSAllowedOrigins: cfg.CORSAllowedOrigins,
			SuggestRateLimit:   cfg.SuggestRateLimit,
			SuggestRateWindow:  cfg.SuggestRateWindow,
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		slog.Info("api listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "err", err)
	}
}

// buildVerifier returns an OIDC verifier when an audience is configured, else a
// deny verifier so the server still boots locally (auth endpoints return 401).
func buildVerifier(ctx context.Context, cfg config.Config) auth.TokenVerifier {
	if cfg.OIDCAudience == "" {
		slog.Warn("OIDC_AUDIENCE unset — auth disabled (auth endpoints return 401)")
		return auth.NewDenyVerifier()
	}
	v, err := auth.NewOIDCVerifier(ctx, cfg.OIDCIssuer, cfg.OIDCAudience)
	if err != nil {
		slog.Error("oidc", "err", err)
		os.Exit(1)
	}
	return v
}
