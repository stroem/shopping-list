// Command lambda serves the same router as cmd/api behind API Gateway (HTTP API
// v2) via the aws-lambda-go-api-proxy adapter. The Neon URL comes from env
// DATABASE_URL or, in AWS, the SSM SecureString named by DATABASE_URL_PARAM.
package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"

	"github.com/stroem/shopping-list/backend/internal/auth"
	"github.com/stroem/shopping-list/backend/internal/db"
	"github.com/stroem/shopping-list/backend/internal/households"
	"github.com/stroem/shopping-list/backend/internal/router"
	"github.com/stroem/shopping-list/backend/internal/suggest"
)

func main() {
	// Bound the whole cold start (AWS config + SSM fetch + pool connect/ping).
	initCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	awsCfg, err := awscfg.LoadDefaultConfig(initCtx)
	if err != nil {
		log.Fatalf("aws config: %v", err)
	}
	databaseURL, err := resolveDatabaseURL(initCtx, os.Getenv, ssm.NewFromConfig(awsCfg))
	if err != nil {
		log.Fatalf("database url: %v", err)
	}

	pool, err := db.NewPool(initCtx, databaseURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}

	verifier := auth.TokenVerifier(auth.NewDenyVerifier())
	if aud := os.Getenv("OIDC_AUDIENCE"); aud != "" {
		issuer := os.Getenv("OIDC_ISSUER")
		if issuer == "" {
			issuer = "https://accounts.google.com"
		}
		v, err := auth.NewOIDCVerifier(initCtx, issuer, aud)
		if err != nil {
			log.Fatalf("oidc: %v", err)
		}
		verifier = v
	}

	adapter := httpadapter.NewV2(router.New(router.Deps{
		DB:             pool,
		Suggest:        suggest.New(pool),
		AuthMiddleware: auth.Middleware(verifier, auth.NewUserStore(pool)),
		Households:     households.NewStore(pool),
	}))
	lambda.Start(adapter.ProxyWithContext)
}
