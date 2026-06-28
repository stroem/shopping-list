# Monorepo Scaffold Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Lay down the empty-but-buildable monorepo skeleton (Go backend, Flutter app, infra placeholder, env config) so every later M0–M3 issue has a home.

**Architecture:** A Go module under `backend/` exposes a single `chi` router (`internal/router`) consumed by three thin entrypoints (`cmd/api` local server, `cmd/lambda` API-Gateway adapter, `cmd/seed` stub). A bare Flutter project under `app/` builds for web + Android. `infra/` is a README placeholder; real IaC and DB wiring belong to later tickets.

**Tech Stack:** Go 1.26 · chi v5 · aws-lambda-go + aws-lambda-go-api-proxy · Flutter 3.41.9 (fvm-pinned) / Dart 3.11.

## Global Constraints

- Go module path: `github.com/stroem/shopping-list/backend`; `go 1.26`.
- Flutter: app id/org `dev.limer.shopping_list`, display name "Shopping List", pub package `shopping_list`, platforms **web + android only**.
- **Wiring only — no business logic.** No pgx/DB, no IaC code, no Riverpod/Drift/go_router (owned by #2/#3/#4/#13).
- `data/` stays gitignored and is never touched.
- Conventional Commits 1.0.0; **no AI/Claude attribution**; commit on branch `chore/issue-1-scaffold-monorepo` (never `main`).
- Green bar: `go test ./...` in `backend/` + `flutter test` in `app/`; build bar: `flutter build web`.

---

### Task 1: Go backend module + chi health router (TDD)

**Files:**
- Create: `backend/go.mod`, `backend/internal/router/router.go`
- Test: `backend/internal/router/router_test.go`

**Interfaces:**
- Produces: `router.New() http.Handler` — a `chi` router serving `GET /healthz` → `200`, body exactly `ok`. Consumed by all three `cmd/*` entrypoints in Task 2.

- [ ] **Step 1: Initialise the module and add chi**

```bash
cd backend
go mod init github.com/stroem/shopping-list/backend
go get github.com/go-chi/chi/v5@latest
```

- [ ] **Step 2: Write the failing test**

Create `backend/internal/router/router_test.go`:

```go
package router

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthz(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	New().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != "ok" {
		t.Fatalf("body = %q, want %q", got, "ok")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd backend && go test ./internal/router/`
Expected: FAIL — `undefined: New` (build error).

- [ ] **Step 4: Write minimal implementation**

Create `backend/internal/router/router.go`:

```go
// Package router builds the HTTP routing shared by the local server and the
// Lambda entrypoint, so the two can never diverge.
package router

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// New returns the application's HTTP handler. It deliberately has no database
// dependency yet — a DB-backed health check arrives with the persistence layer.
func New() http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return r
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd backend && go test ./... && go vet ./...`
Expected: PASS; vet clean.

- [ ] **Step 6: Commit**

```bash
git add backend/go.mod backend/go.sum backend/internal/router/
git commit -m "feat(backend): add chi router with healthz endpoint"
```

---

### Task 2: Backend entrypoints (api, lambda, seed)

**Files:**
- Create: `backend/cmd/api/main.go`, `backend/cmd/lambda/main.go`, `backend/cmd/seed/main.go`, `backend/migrations/.gitkeep`

**Interfaces:**
- Consumes: `router.New() http.Handler` from Task 1.
- Produces: three buildable `main` packages; `cmd/api` serves on `PORT` (default `8080`).

- [ ] **Step 1: Add the Lambda adapter dependencies**

```bash
cd backend
go get github.com/aws/aws-lambda-go@latest
go get github.com/awslabs/aws-lambda-go-api-proxy/httpadapter@latest
```

- [ ] **Step 2: Write `cmd/api/main.go`**

```go
// Command api runs the HTTP backend locally. It is the local-dev baseline and
// shares its router with the Lambda entrypoint.
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/stroem/shopping-list/backend/internal/router"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port
	log.Printf("api listening on %s", addr)
	if err := http.ListenAndServe(addr, router.New()); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
```

- [ ] **Step 3: Write `cmd/lambda/main.go`**

```go
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
```

- [ ] **Step 4: Write `cmd/seed/main.go` (stub)**

```go
// Command seed will import data/food/* into Postgres (catalog + EAN mappings).
// It is a stub until the persistence and catalog tickets land.
package main

import "log"

func main() {
	log.Println("seed: not implemented yet")
}
```

- [ ] **Step 5: Reserve the migrations directory**

```bash
mkdir -p backend/migrations && touch backend/migrations/.gitkeep
```

- [ ] **Step 6: Build and vet everything**

Run: `cd backend && go build ./... && go vet ./... && go test ./...`
Expected: all succeed; router test still passes.

- [ ] **Step 7: Smoke-test the local server**

Run:
```bash
cd backend && (go run ./cmd/api &) && sleep 2 && curl -s localhost:8080/healthz && echo && pkill -f 'cmd/api'
```
Expected: prints `ok`.

- [ ] **Step 8: Commit**

```bash
git add backend/cmd backend/migrations backend/go.mod backend/go.sum
git commit -m "feat(backend): add api, lambda, and seed entrypoints"
```

---

### Task 3: Flutter app scaffold (web + android)

**Files:**
- Create: `app/` (generated by `flutter create`), trimmed to web + android.

**Interfaces:**
- Produces: a Flutter project that builds for web and runs on Android; its default `widget_test.dart` passes under `flutter test`.

- [ ] **Step 1: Generate the project**

```bash
flutter create --org dev.limer --project-name shopping_list \
  --platforms web,android --description "Shopping List" app
```

- [ ] **Step 2: Confirm only web + android platforms exist**

Run: `ls app/` — expect `android/`, `web/`, `lib/`, `pubspec.yaml`, `test/`; no `ios/`, `linux/`, `macos/`, `windows/`. (If `flutter create` emitted extras despite `--platforms`, remove the unwanted platform directories.)

- [ ] **Step 3: Run the generated test**

Run: `cd app && flutter test`
Expected: PASS (the default counter smoke test).

- [ ] **Step 4: Build for web**

Run: `cd app && flutter build web`
Expected: succeeds; `app/build/web/` produced (build output is gitignored).

- [ ] **Step 5: Commit**

```bash
git add app
git commit -m "feat(app): scaffold flutter project for web and android"
```

---

### Task 4: Repo wiring — env, fvm pin, infra placeholder, README

**Files:**
- Create: `.env.example`, `.fvmrc`, `infra/README.md`, `README.md`
- Modify: `.gitignore` (add `.fvm/`)

**Interfaces:**
- Produces: documented entrypoints for env config, Flutter version pin, and infra; nothing imported by code.

- [ ] **Step 1: Write `.env.example`**

```bash
# Local backend configuration. Copy to .env and fill in.
# Postgres connection string (local or Neon). Not used until the DB wiring ticket (#3).
DATABASE_URL=postgres://localhost:5432/shopping_list?sslmode=disable
# Port for `go run ./cmd/api` (default 8080).
PORT=8080
```

- [ ] **Step 2: Pin Flutter via `.fvmrc`**

Create `.fvmrc`:

```json
{
  "flutter": "3.41.9"
}
```

- [ ] **Step 3: Ignore the fvm cache**

Append to `.gitignore` (under the existing Flutter section):

```
# fvm
.fvm/
```

- [ ] **Step 4: Write `infra/README.md`**

```markdown
# infra/

Infrastructure-as-code for deploying the backend as an **AWS Lambda behind an
API Gateway HTTP API**, connected to **Neon Postgres** (scale-to-zero).

This directory is a placeholder. The actual IaC (SAM or Terraform) lands with
[#4 — Lambda adapter + API Gateway + Neon wiring](https://github.com/stroem/shopping-list/issues/4).
Design constraint: keep running cost ≈ 0 (no always-on compute).
```

- [ ] **Step 5: Write a short top-level `README.md`**

```markdown
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
```

- [ ] **Step 6: Verify the full green bar**

Run:
```bash
cd backend && go build ./... && go vet ./... && go test ./... && cd ../app && flutter test && flutter build web
```
Expected: all succeed.

- [ ] **Step 7: Commit**

```bash
git add .env.example .fvmrc .gitignore infra/README.md README.md
git commit -m "chore: add env example, fvm pin, infra placeholder, and readme"
```

---

## Self-Review

**Spec coverage:**
- `backend/` Go module with `cmd/api`, `cmd/lambda`, `cmd/seed`, `internal/`, `migrations/` → Tasks 1–2. ✓
- `app/` Flutter project (web + android) → Task 3. ✓
- `infra/` and `docs/` exist; `.env.example` documents `DATABASE_URL` → Task 4 (`docs/` already exists). ✓
- `go build ./...` and `flutter build web` succeed → Task 4 Step 6. ✓
- Standard Go layout, wiring only, `data/` gitignored → Global Constraints; no DB/IaC/state-libs added. ✓

**Placeholder scan:** The only "not implemented" is the intentional `cmd/seed` stub, described as such. No TBD/TODO in the plan steps. ✓

**Type consistency:** `router.New() http.Handler` defined in Task 1 is consumed identically by `cmd/api`, `cmd/lambda`, and the test. `httpadapter.NewV2` + `ProxyWithContext` are the adapter's real API. ✓
