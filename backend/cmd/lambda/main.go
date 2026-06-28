// Command lambda serves the same router as cmd/api behind API Gateway (HTTP API
// v2) via the aws-lambda-go-api-proxy adapter. The Neon URL comes from env
// DATABASE_URL or, in AWS, the SSM SecureString named by DATABASE_URL_PARAM.
package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"

	"github.com/stroem/shopping-list/backend/internal/auth"
	"github.com/stroem/shopping-list/backend/internal/config"
	"github.com/stroem/shopping-list/backend/internal/db"
	"github.com/stroem/shopping-list/backend/internal/households"
	"github.com/stroem/shopping-list/backend/internal/logging"
	"github.com/stroem/shopping-list/backend/internal/router"
	"github.com/stroem/shopping-list/backend/internal/suggest"
)

func main() {
	slog.SetDefault(logging.New(os.Stderr, os.Getenv("LOG_LEVEL")))

	// Bound the whole cold start (AWS config + SSM fetch + pool connect/ping).
	initCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	awsCfg, err := awscfg.LoadDefaultConfig(initCtx)
	if err != nil {
		slog.Error("aws config", "err", err)
		os.Exit(1)
	}
	databaseURL, err := resolveDatabaseURL(initCtx, os.Getenv, ssm.NewFromConfig(awsCfg))
	if err != nil {
		slog.Error("database url", "err", err)
		os.Exit(1)
	}

	pool, err := db.NewPool(initCtx, databaseURL)
	if err != nil {
		slog.Error("database", "err", err)
		os.Exit(1)
	}

	verifier := auth.TokenVerifier(auth.NewDenyVerifier())
	if aud := os.Getenv("OIDC_AUDIENCE"); aud != "" {
		issuer := os.Getenv("OIDC_ISSUER")
		if issuer == "" {
			issuer = "https://accounts.google.com"
		}
		v, err := auth.NewOIDCVerifier(initCtx, issuer, aud)
		if err != nil {
			slog.Error("oidc", "err", err)
			os.Exit(1)
		}
		verifier = v
	}

	// Lambda skips config.Load; read the CORS origins straight from env, parsed
	// the same way so cmd/api and cmd/lambda behave identically.
	corsOrigins := config.ParseCORSOrigins(os.Getenv("CORS_ALLOWED_ORIGINS"))
	adapter := httpadapter.NewV2(router.New(router.Deps{
		DB:                 pool,
		Suggest:            suggest.New(pool),
		AuthMiddleware:     auth.Middleware(verifier, auth.NewUserStore(pool)),
		Households:         households.NewStore(pool),
		CORSAllowedOrigins: corsOrigins,
	}))
	lambda.Start(adapter.ProxyWithContext)
}
