# infra/

AWS **SAM** IaC: the Go backend as a single **arm64 Lambda** behind an **API
Gateway HTTP API**, connected to **Neon Postgres**. Cost-minimal: `provided.al2023`,
128 MB, no VPC (reaches Neon over public TLS, so no NAT gateway), scale-to-zero.

- `template.yaml` — the SAM stack (Lambda + HTTP API + SSM read policy).
- `../backend/Makefile` — `build-ApiFunction` cross-compiles `bootstrap` (arm64);
  invoked by SAM because the function's `CodeUri` is `../backend`.
- `samconfig.toml.example` — copy to `samconfig.toml` (gitignored) for deploy defaults.

## Prerequisites

- A **Neon** project; copy its **pooled** connection string.
- AWS credentials configured (`aws configure` / SSO), region `eu-west-1`.
- `sam` (AWS SAM CLI) and Go installed.

## One-time: store the secret

The connection string is a secret (it holds the DB password); it never goes in the
template. Store it as an SSM SecureString — the Lambda fetches it at cold start:

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

- HTTP API (not REST) for cost; payload format 2.0 matches `httpadapter.NewV2`.
- The Lambda resolves the URL from `DATABASE_URL` (if set) else the
  `DATABASE_URL_PARAM` SSM SecureString at cold start.
- A full-featured migration command is tracked in #25.
