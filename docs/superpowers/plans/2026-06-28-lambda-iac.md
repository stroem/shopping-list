# Lambda + API Gateway + Neon IaC Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deploy the Go backend as a single arm64 AWS Lambda behind an API Gateway HTTP API, connected to Neon, defined as validated AWS SAM IaC with a documented one-command deploy.

**Architecture:** `cmd/lambda` resolves the Neon URL at cold start (env, else SSM SecureString) and serves the chi router via the v2 adapter. `infra/template.yaml` (SAM) provisions the Lambda + HTTP API, injecting only the SSM *parameter name*; `infra/Makefile` cross-compiles `bootstrap`. Validated with `sam validate`/`sam build`; the live deploy is the operator's documented step.

**Tech Stack:** AWS SAM (CloudFormation) · Lambda `provided.al2023` arm64 · API Gateway HTTP API · aws-sdk-go-v2 (config, ssm) · Go 1.26.

## Global Constraints

- Go module `github.com/stroem/shopping-list/backend`; `go 1.26`.
- Lambda: `provided.al2023`, **arm64**, `MemorySize 128`, `Timeout 10`; **no VPC**; HTTP API ($default, payload v2).
- Secret never in the template: Lambda fetches the **SSM SecureString** named by `DATABASE_URL_PARAM` at cold start (env `DATABASE_URL` wins if set).
- Region default **eu-west-1**; param path default **/shopping-list/database-url**.
- Deliverable is validated IaC + docs; **no live `sam deploy`** in this issue.
- DB-backed tests skip without docker; the new Go test needs no network.
- Conventional Commits; **no AI attribution**; commit on `feat/issue-4-lambda-iac` (never `main`).

## File structure

- `backend/cmd/lambda/resolve.go` (+ `resolve_test.go`) — DB-URL resolution (env | SSM).
- `backend/cmd/lambda/main.go` (modify) — wire resolver + bounded cold-start pool.
- `infra/template.yaml` (create) — SAM stack.
- `infra/Makefile` (create) — arm64 build of `bootstrap`.
- `infra/README.md` (replace) — deploy/teardown docs.
- `infra/samconfig.toml.example` (create) — non-secret deploy defaults (the real `samconfig.toml` is gitignored).

---

### Task 1: Cold-start DB-URL resolution (env | SSM) — TDD

**Files:**
- Create: `backend/cmd/lambda/resolve.go`, `backend/cmd/lambda/resolve_test.go`

**Interfaces:**
- Produces:
  - `type paramGetter interface { GetParameter(ctx, *ssm.GetParameterInput, ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) }`
  - `func resolveDatabaseURL(ctx context.Context, env func(string) string, ssmc paramGetter) (string, error)`

- [ ] **Step 1: Add the SDK dependencies**

```bash
cd backend
go get github.com/aws/aws-sdk-go-v2/config@latest
go get github.com/aws/aws-sdk-go-v2/service/ssm@latest
```

- [ ] **Step 2: Write the failing test**

`backend/cmd/lambda/resolve_test.go`:

```go
package main

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

type fakeSSM struct {
	out *ssm.GetParameterOutput
	err error
	got string // captured parameter name
}

func (f *fakeSSM) GetParameter(_ context.Context, in *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	if in.Name != nil {
		f.got = *in.Name
	}
	return f.out, f.err
}

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestResolvePrefersEnv(t *testing.T) {
	f := &fakeSSM{err: errors.New("should not be called")}
	got, err := resolveDatabaseURL(context.Background(),
		env(map[string]string{"DATABASE_URL": "postgres://from-env"}), f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "postgres://from-env" {
		t.Fatalf("got %q, want env value", got)
	}
	if f.got != "" {
		t.Fatalf("SSM should not have been called, but got name %q", f.got)
	}
}

func TestResolveFetchesFromSSM(t *testing.T) {
	f := &fakeSSM{out: &ssm.GetParameterOutput{
		Parameter: &ssmParam("postgres://from-ssm"),
	}}
	got, err := resolveDatabaseURL(context.Background(),
		env(map[string]string{"DATABASE_URL_PARAM": "/shopping-list/database-url"}), f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "postgres://from-ssm" {
		t.Fatalf("got %q, want ssm value", got)
	}
	if f.got != "/shopping-list/database-url" {
		t.Fatalf("requested param %q", f.got)
	}
}

func TestResolveSSMErrorPropagates(t *testing.T) {
	f := &fakeSSM{err: errors.New("boom")}
	_, err := resolveDatabaseURL(context.Background(),
		env(map[string]string{"DATABASE_URL_PARAM": "/p"}), f)
	if err == nil {
		t.Fatal("expected error from SSM, got nil")
	}
}

func TestResolveMissingConfig(t *testing.T) {
	_, err := resolveDatabaseURL(context.Background(), env(map[string]string{}), &fakeSSM{})
	if err == nil {
		t.Fatal("expected error when neither DATABASE_URL nor DATABASE_URL_PARAM set")
	}
}

// ssmParam is a tiny helper to build an *types.Parameter with a value.
func ssmParam(v string) ssmtypesParameter { return ssmtypesParameter{Value: aws.String(v)} }
```

Note: the helper in the test references `ssmtypesParameter` — replace it in Step 4 with the real type once the import is known. (See Step 4; the real test uses `types.Parameter`.)

- [ ] **Step 3: Run — expect FAIL** (`undefined: resolveDatabaseURL`). `cd backend && go test ./cmd/lambda/`

- [ ] **Step 4: Fix the test's parameter helper to the real SDK type**

Replace the `ssmParam` helper and the `fakeSSM.out` construction to use `github.com/aws/aws-sdk-go-v2/service/ssm/types`:

```go
import (
	// ...existing...
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// in TestResolveFetchesFromSSM:
	f := &fakeSSM{out: &ssm.GetParameterOutput{
		Parameter: &types.Parameter{Value: aws.String("postgres://from-ssm")},
	}}

// remove the ssmParam helper / ssmtypesParameter entirely.
```

- [ ] **Step 5: Implement `backend/cmd/lambda/resolve.go`**

```go
package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// paramGetter is the slice of the SSM API this command uses. The real
// *ssm.Client satisfies it; tests pass a fake.
type paramGetter interface {
	GetParameter(ctx context.Context, in *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// resolveDatabaseURL returns the Postgres URL with precedence:
//  1. env DATABASE_URL (local/testing),
//  2. the SSM SecureString named by env DATABASE_URL_PARAM (decrypted).
// It errors if neither is configured.
func resolveDatabaseURL(ctx context.Context, env func(string) string, ssmc paramGetter) (string, error) {
	if url := env("DATABASE_URL"); url != "" {
		return url, nil
	}
	name := env("DATABASE_URL_PARAM")
	if name == "" {
		return "", errors.New("neither DATABASE_URL nor DATABASE_URL_PARAM is set")
	}
	out, err := ssmc.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(name),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return "", fmt.Errorf("fetch %s from ssm: %w", name, err)
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return "", fmt.Errorf("ssm parameter %s has no value", name)
	}
	return *out.Parameter.Value, nil
}
```

- [ ] **Step 6: Run — expect PASS.** `cd backend && go test ./cmd/lambda/ && go vet ./cmd/lambda/`

- [ ] **Step 7: Commit**

```bash
git add backend/cmd/lambda/resolve.go backend/cmd/lambda/resolve_test.go backend/go.mod backend/go.sum
git commit -m "feat(lambda): resolve database url from env or ssm securestring"
```

---

### Task 2: Wire the resolver into `cmd/lambda/main.go`

**Files:**
- Modify: `backend/cmd/lambda/main.go`

**Interfaces:**
- Consumes: `resolveDatabaseURL`, `db.NewPool`, `router.New`, `config` (no longer needed for the URL).

- [ ] **Step 1: Rewrite `backend/cmd/lambda/main.go`**

```go
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
```

- [ ] **Step 2: Build for the Lambda target + vet + full test**

Run:
```bash
cd backend && GOOS=linux GOARCH=arm64 go build -tags lambda.norpc -o /dev/null ./cmd/lambda \
  && go build ./... && go vet ./... && go test ./...
```
Expected: cross-compile succeeds; all tests green.

- [ ] **Step 3: Commit**

```bash
git add backend/cmd/lambda/main.go
git commit -m "feat(lambda): bounded cold-start pool init via resolved url"
```

---

### Task 3: SAM template + Makefile + samconfig example

**Files:**
- Create: `infra/template.yaml`, `infra/Makefile`, `infra/samconfig.toml.example`

**Interfaces:** infra only; the Makefile target name `build-ApiFunction` must match the function logical id `ApiFunction`.

- [ ] **Step 1: Write `infra/template.yaml`**

```yaml
AWSTemplateFormatVersion: "2010-09-09"
Transform: AWS::Serverless-2016-10-09
Description: Shopping List backend — Go Lambda behind an HTTP API, Neon Postgres.

Parameters:
  DatabaseUrlParam:
    Type: String
    Default: /shopping-list/database-url
    Description: SSM SecureString parameter name holding the Neon pooled connection URL.

Globals:
  Function:
    Runtime: provided.al2023
    Architectures: [arm64]
    MemorySize: 128
    Timeout: 10

Resources:
  ApiFunction:
    Type: AWS::Serverless::Function
    Metadata:
      BuildMethod: makefile
    Properties:
      CodeUri: .
      Handler: bootstrap
      Environment:
        Variables:
          DATABASE_URL_PARAM: !Ref DatabaseUrlParam
      Policies:
        - SSMParameterReadPolicy:
            ParameterName: !Ref DatabaseUrlParam
      Events:
        Api:
          Type: HttpApi

Outputs:
  ApiUrl:
    Description: Base URL of the HTTP API.
    Value: !Sub "https://${ServerlessHttpApi}.execute-api.${AWS::Region}.amazonaws.com"
```

- [ ] **Step 2: Write `infra/Makefile`**

```make
# SAM makefile builder target. SAM sets ARTIFACTS_DIR and expects `bootstrap` there.
build-ApiFunction:
	cd ../backend && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
		go build -tags lambda.norpc -o "$(ARTIFACTS_DIR)/bootstrap" ./cmd/lambda
```

(Use a literal TAB for the recipe indentation.)

- [ ] **Step 3: Write `infra/samconfig.toml.example`**

```toml
# Copy to samconfig.toml (gitignored) and adjust. Used by `sam deploy`.
version = 0.1

[default.deploy.parameters]
stack_name = "shopping-list"
region = "eu-west-1"
capabilities = "CAPABILITY_IAM"
resolve_s3 = true
confirm_changeset = true
```

- [ ] **Step 4: Validate the template (no AWS creds needed)**

Run: `cd infra && sam validate --lint --region eu-west-1`
Expected: `template.yaml is a valid SAM Template` (lint reports no findings).

- [ ] **Step 5: Build (cross-compiles bootstrap via the Makefile)**

Run: `cd infra && sam build`
Expected: `Build Succeeded`; `.aws-sam/build/ApiFunction/bootstrap` exists.

- [ ] **Step 6: Commit**

```bash
git add infra/template.yaml infra/Makefile infra/samconfig.toml.example
git commit -m "feat(infra): sam template for lambda + http api + ssm"
```

---

### Task 4: Deploy/teardown docs

**Files:**
- Modify: `infra/README.md`

- [ ] **Step 1: Replace `infra/README.md`**

````markdown
# infra/

AWS **SAM** IaC: the Go backend as a single **arm64 Lambda** behind an **API
Gateway HTTP API**, connected to **Neon Postgres**. Cost-minimal: 128 MB, no VPC
(reaches Neon over public TLS, so no NAT gateway), scale-to-zero.

## Prerequisites

- A **Neon** project; copy its **pooled** connection string.
- AWS credentials configured (`aws configure` / SSO), region `eu-west-1`.
- `sam` (AWS SAM CLI) and Go installed.

## One-time: store the secret

The connection string is a secret (it holds the DB password); it never goes in
the template. Store it as an SSM SecureString:

```bash
aws ssm put-parameter --region eu-west-1 --type SecureString \
  --name /shopping-list/database-url \
  --value 'postgres://USER:PASSWORD@ep-xxx-pooler.eu-west-1.aws.neon.tech/shopping_list?sslmode=require'
```

## Deploy

```bash
cd infra
cp samconfig.toml.example samconfig.toml   # first time only
sam build
sam deploy --guided                        # first time; then just `sam deploy`
```

`sam deploy` prints the **ApiUrl** output. Smoke-test it:

```bash
curl "$API_URL/healthz"     # -> {"status":"ok"}
```

## Migrations (release step)

Run migrations against Neon from `backend/` (uses the same URL):

```bash
DATABASE_URL='<neon-pooled-url>' go run ./cmd/migrate up
```

## Teardown (leaves ~0 ongoing cost)

```bash
cd infra && sam delete
# optional: remove the secret
aws ssm delete-parameter --region eu-west-1 --name /shopping-list/database-url
```

## Notes

- Lambda runtime `provided.al2023`, arm64; HTTP API (not REST) for cost.
- The Lambda resolves the URL from `DATABASE_URL` (if set) else the
  `DATABASE_URL_PARAM` SSM SecureString at cold start.
- Full-featured migration command is tracked in #25.
````

- [ ] **Step 2: Commit**

```bash
git add infra/README.md
git commit -m "docs(infra): document one-command deploy and teardown"
```

---

## Self-Review

**Spec coverage:**
- `cmd/lambda` wraps the chi adapter (AC1) — preserved; resolver added → Tasks 1–2. ✓
- IaC under `infra/` provisions Lambda + HTTP API + env wiring (AC2) → Task 3 (`template.yaml`). ✓
- Neon pooled URL; scales to zero, no always-on (AC3) → SSM SecureString + no VPC + arm64/128MB; README requires the pooled host. ✓
- Documented one-command deploy; teardown ~0 cost (AC4) → Task 4 README (`sam build && sam deploy`, `sam delete`). ✓
- Secret handling (env|SSM at cold start) → Tasks 1–2 + template injects only the param name. ✓
- Validation boundary (sam validate/build, no deploy) → Task 3 Steps 4–5. ✓

**Placeholder scan:** Task 1's test deliberately introduces a wrong helper type at Step 2 and fixes it at Step 4 (TDD red→correct); that is an explicit, completed instruction, not a leftover TODO. No other TBD/TODO. ✓

**Type consistency:** `resolveDatabaseURL(ctx, env func(string) string, ssmc paramGetter)` and `paramGetter.GetParameter(...)` match between `resolve.go`, the test, and `main.go` (which passes `os.Getenv` and `ssm.NewFromConfig(...)`). The Makefile target `build-ApiFunction` matches the template's `ApiFunction` logical id. The `ServerlessHttpApi` logical id in Outputs is SAM's implicit HTTP API id for an `HttpApi` event. ✓
