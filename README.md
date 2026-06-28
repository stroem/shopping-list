# Shopping List ("Handla")

A shared **food** shopping list that learns what your household buys and sorts by
store aisle. Flutter app (web + Android) on a Go backend deployed as an AWS Lambda
behind API Gateway, backed by Neon Postgres. See [`AGENTS.md`](./AGENTS.md) and
[`docs/superpowers/specs/2026-06-28-handla-food-mvp-design.md`](./docs/superpowers/specs/2026-06-28-handla-food-mvp-design.md).

## Layout

- `backend/` — Go module (`cmd/api` local server, `cmd/lambda` adapter, `cmd/seed`, `internal/`, `migrations/`).
- `app/` — Flutter app (web + Android).
- `infra/` — IaC for Lambda + API Gateway + Neon (from #4).
- `data/` — downloaded source data, **gitignored**, imported once by `cmd/seed`.

## Develop

```bash
# Backend
cd backend && go run ./cmd/api      # http://localhost:8080/healthz -> ok
cd backend && go test ./...

# App (fvm-pinned Flutter 3.41.9)
cd app && flutter run -d chrome
cd app && flutter test
```
