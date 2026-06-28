# Monorepo scaffold (issue #1) — design

**Status:** approved 2026-06-28
**Issue:** [#1 — Scaffold the monorepo](https://github.com/stroem/shopping-list/issues/1)
(milestone *M0 — Foundation & infra*)
**Parent vision:**
[`2026-06-28-handla-food-mvp-design.md`](./2026-06-28-handla-food-mvp-design.md)

## Goal

Lay down the empty-but-buildable monorepo skeleton so every later M0–M3 issue has
a home. **Wiring only — no business logic.** Real DB wiring, IaC, and app state
libraries are owned by later tickets and are deliberately excluded here.

## Bake-in decisions

- **Go module path:** `github.com/stroem/shopping-list/backend` (`go 1.26`).
- **Flutter app id / org:** `dev.limer.shopping_list`; display name "Shopping
  List"; pub package `shopping_list`.
- **Lambda adapter:** awslabs `aws-lambda-go-api-proxy/httpadapter` wrapping the
  same chi router the local server uses.
- **Flutter pin:** `.fvmrc` → Flutter `3.41.9` (matches the dev machine).

## Layout produced

```
backend/                         Go module: github.com/stroem/shopping-list/backend
  go.mod  go.sum
  internal/router/router.go      chi router; GET /healthz -> 200 "ok"
  internal/router/router_test.go test: /healthz returns 200 "ok"
  cmd/api/main.go                local HTTP server on :8080 (env PORT), serves router
  cmd/lambda/main.go             wraps the SAME router via httpadapter + lambda.Start
  cmd/seed/main.go               prints "seed: not implemented yet" (real work in #5/#6)
  migrations/.gitkeep            reserved for #2
app/                             bare `flutter create`, platforms web + android only
infra/README.md                 placeholder; real Lambda+API Gateway+Neon IaC is #4
.env.example                     documents DATABASE_URL and PORT
.fvmrc                           { "flutter": "3.41.9" }
.gitignore                       extended for .fvm/
```

## What each unit does

- **`internal/router`** — the single source of truth for HTTP routing. Builds a
  `chi.Router` with `GET /healthz` → `200` body `ok`. It does **not** touch a
  database (DB wiring is #3). Both entrypoints consume it, so local and Lambda
  behaviour can never diverge.
- **`cmd/api`** — reads `PORT` (default `8080`) and serves `router.New()` with the
  stdlib HTTP server. This is the local-dev baseline.
- **`cmd/lambda`** — wraps `router.New()` in `httpadapter.NewV2(...)` and calls
  `lambda.Start`. No AWS resources are created here; that is #4.
- **`cmd/seed`** — a stub `main` that prints a not-implemented notice and exits 0,
  so the binary builds and the import path exists for #5/#6.
- **`app/`** — a bare Flutter project that builds for web and Android. No state
  management, persistence, or routing libraries yet — those arrive in #13.
- **`infra/README.md`** — a short note that IaC lives here from #4 onward.

## Out of scope (owned elsewhere)

Postgres/pgx wiring and a DB-backed health check (#3) · schema/migrations (#2) ·
real Lambda + API Gateway + Neon IaC (#4) · Riverpod/Drift/go_router (#13) · CI
workflow (possible follow-up). Pulling any of these into #1 is scope creep.

## Verification (acceptance criteria)

- `cd backend && go build ./... && go vet ./... && go test ./...` — green
  (router test passes).
- `cd app && flutter build web` — succeeds.
- `cd backend && go run ./cmd/api` then `curl -s localhost:8080/healthz` → `ok`.

Green bar for this issue = `go test ./...` in `backend/` **and** `flutter test`
in `app/`. The bare `flutter create` output is kept as-is, including its default
`widget_test.dart` smoke test (which passes against the generated `main.dart`), so
`flutter test` is green without writing app logic. `flutter build web` is the
build-level bar.

## Testing approach

TDD where it has signal: write `internal/router/router_test.go` asserting
`/healthz` returns `200 "ok"` **before** implementing `router.New()`. The
entrypoints (`cmd/*`) and the Flutter scaffold are wiring with no branching logic
— they are verified by `go build` / `flutter build`, not unit tests.
