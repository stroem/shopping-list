// Command lambda serves the same router as cmd/api behind API Gateway (HTTP API
// v2) via the aws-lambda-go-api-proxy adapter. It requires DATABASE_URL.
package main

import (
	"context"
	"log"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"

	"github.com/stroem/shopping-list/backend/internal/config"
	"github.com/stroem/shopping-list/backend/internal/db"
	"github.com/stroem/shopping-list/backend/internal/router"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	pool, err := db.NewPool(context.Background(), cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	adapter := httpadapter.NewV2(router.New(router.Deps{DB: pool}))
	lambda.Start(adapter.ProxyWithContext)
}
