# Lambda + API Gateway + Neon IaC (issue #4) — design

**Status:** approved 2026-06-28
**Issue:** [#4 — Lambda adapter + API Gateway (HTTP API) + Neon wiring as IaC](https://github.com/stroem/shopping-list/issues/4)
(milestone *M0 — Foundation & infra*)
**Builds on:** #1 (`cmd/lambda`), #3 (router, pool, config).

## Goal

Deploy the Go backend as a single AWS Lambda behind an API Gateway **HTTP API**,
connected to **Neon Postgres**, defined as **AWS SAM** IaC under `infra/`. Cost is
the constraint: arm64, 128 MB, no VPC/NAT, scale-to-zero — ~0 when idle.

**Deliverable boundary:** this issue delivers and *validates* the IaC
(`sam validate`, `sam build`) plus a documented one-command deploy/teardown. The
live `sam deploy` is the operator's step (needs AWS credentials + a Neon project);
it is **not** run as part of this issue.

## Key decision — secret handling

CloudFormation does **not** allow an `ssm-secure` dynamic reference in a Lambda
environment variable (it would expose the secret in plaintext). To keep the Neon
connection string a real secret (it contains the DB password), the Lambda
**resolves it at cold start from SSM**:

- The template passes the **parameter name** (`DATABASE_URL_PARAM`), never the value.
- At cold start `cmd/lambda` reads `DATABASE_URL` if set (local/testing), otherwise
  fetches the `DATABASE_URL_PARAM` SecureString from SSM with decryption.
- IAM: SAM `SSMParameterReadPolicy` for that parameter. The AWS-managed `aws/ssm`
  key needs no extra `kms:Decrypt` grant.

## Components

### `infra/template.yaml` (SAM)
- `Transform: AWS::Serverless-2016-10-09`.
- **Parameters:** `DatabaseUrlParam` (String, default `/shopping-list/database-url`).
- **Globals → Function:** `Runtime: provided.al2023`, `Architectures: [arm64]`,
  `MemorySize: 128`, `Timeout: 10`.
- **`ApiFunction` (AWS::Serverless::Function):**
  - `Handler: bootstrap`, `CodeUri: .`, `Metadata: { BuildMethod: makefile }`.
  - `Environment.Variables.DATABASE_URL_PARAM: !Ref DatabaseUrlParam`.
  - `Policies: [ { SSMParameterReadPolicy: { ParameterName: !Ref DatabaseUrlParam } } ]`.
  - `Events.Api: { Type: HttpApi }` (no `Path`/`Method` → catch-all `$default`,
    payload format 2.0, matching `httpadapter.NewV2`).
- **Implicit `AWS::Serverless::HttpApi`** (created from the HttpApi event).
- **Outputs:** `ApiUrl` — the HTTP API base URL.
- No VPC config ⇒ Lambda reaches Neon over public TLS; no NAT gateway.

### `infra/Makefile`
`build-ApiFunction` target (invoked by SAM's makefile builder) cross-compiles the
Lambda and places `bootstrap` in `$(ARTIFACTS_DIR)`:

```make
build-ApiFunction:
	cd ../backend && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
		go build -tags lambda.norpc -o "$(ARTIFACTS_DIR)/bootstrap" ./cmd/lambda
```

### `backend/cmd/lambda/main.go` (changed) + `resolve.go`
Resolve the database URL with precedence, then build the pool with a **bounded**
cold-start timeout (also closes the #3 review nit), then serve via the adapter.

```go
// resolveDatabaseURL returns os.Getenv("DATABASE_URL") if set; otherwise it
// fetches the SecureString named by DATABASE_URL_PARAM from SSM.
func resolveDatabaseURL(ctx context.Context, env func(string) string, ssm paramGetter) (string, error)

// paramGetter is the slice of the SSM API we use (one method) — fakeable in tests.
type paramGetter interface {
	GetParameter(ctx context.Context, in *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}
```

`main` wires the real `ssm.Client` (from `aws-sdk-go-v2/config.LoadDefaultConfig`).
New deps: `aws-sdk-go-v2/config`, `aws-sdk-go-v2/service/ssm`. Only `cmd/lambda`
imports them, so the `cmd/api` binary is unaffected (Go links per binary).

### `infra/README.md` (replaces placeholder)
Prerequisites (Neon project + **pooled** connection string; AWS creds), then:

```bash
# one-time: store the secret
aws ssm put-parameter --region eu-west-1 --type SecureString \
  --name /shopping-list/database-url --value '<neon-pooled-url>'

# deploy (one command after first --guided run)
sam build && sam deploy --guided      # region eu-west-1

# run migrations as a release step (from backend/)
DATABASE_URL='<neon-pooled-url>' go run ./cmd/migrate up

# teardown — leaves ~0 ongoing cost
sam delete
```

Notes: use the Neon **pooled** host; `provided.al2023` + arm64; HTTP API (not REST)
for cost; no VPC.

## Data flow

```
client → HTTP API ($default) → Lambda (cold start: SSM SecureString → DATABASE_URL → pgx pool)
       → router.New(Deps{DB: pool}) → handler
```

## Testing

- **Go unit test** `resolve_test.go`: env set → returns env value, SSM not called;
  env empty + param set → returns the faked SSM value; SSM error → propagated.
  Pure logic with a fake `paramGetter`; no AWS, no docker.
- **`sam validate --lint`** — template is valid CloudFormation/SAM.
- **`sam build`** — the Makefile cross-compiles `bootstrap` (arm64) successfully.
- Green bar `go test ./...` unaffected (the new test needs no network).

`sam deploy` is **not** part of CI/verification — it is the operator step.

## Out of scope (owned elsewhere)

Full migrate CLI (#25) · CI/CD pipeline · custom domain / TLS cert · provisioned
concurrency or VPC (deliberately avoided for cost) · the actual production deploy
(operator runs it).
