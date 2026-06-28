// Command lambda serves the same router as cmd/api behind API Gateway (HTTP API
// v2) via the aws-lambda-go-api-proxy adapter. The Neon URL comes from env
// DATABASE_URL or, in AWS, the SSM SecureString named by DATABASE_URL_PARAM.
package main

import (
	"context"
	"log"
	"os"
	"time"

	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"

	"github.com/stroem/shopping-list/backend/internal/db"
	"github.com/stroem/shopping-list/backend/internal/router"
)

func main() {
	ctx := context.Background()

	awsCfg, err := awscfg.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("aws config: %v", err)
	}
	databaseURL, err := resolveDatabaseURL(ctx, os.Getenv, ssm.NewFromConfig(awsCfg))
	if err != nil {
		log.Fatalf("database url: %v", err)
	}

	initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pool, err := db.NewPool(initCtx, databaseURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}

	adapter := httpadapter.NewV2(router.New(router.Deps{DB: pool}))
	lambda.Start(adapter.ProxyWithContext)
}
