// Command lambda serves the same router as cmd/api behind API Gateway (HTTP API
// v2) via the aws-lambda-go-api-proxy adapter. No AWS resources are defined here.
package main

import (
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"

	"github.com/stroem/shopping-list/backend/internal/router"
)

func main() {
	adapter := httpadapter.NewV2(router.New())
	lambda.Start(adapter.ProxyWithContext)
}
