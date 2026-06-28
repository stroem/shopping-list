# Local API Server Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the placeholder API into a real local-dev server — env config, a serverless-sized pgx pool, shared HTTP middleware, a DB-backed JSON `/healthz` — plus `cmd/migrate` and a root `justfile` to run it all locally.

**Architecture:** `config` loads env, `db.NewPool` builds the pool, `web` holds shared middleware + JSON helpers, `router.New(Deps{DB: Pinger})` wires them into the chi router that both `cmd/api` and `cmd/lambda` serve. `db.Migrate` is refactored to accept a `postgres://` URL and `cmd/migrate` exposes it on the CLI.

**Tech Stack:** Go 1.26 · chi v5 (+ middleware) · jackc/pgx/v5 + pgxpool · golang-migrate v4 · testcontainers-go · just.

## Global Constraints

- Go module `github.com/stroem/shopping-list/backend`; `go 1.26`.
- **`DATABASE_URL` required** by `cmd/api`/`cmd/migrate`; one format `postgres://…` everywhere.
- `/healthz` returns JSON (`200 {"status":"ok"}` / `503 {"status":"unavailable","error":…}`).
- DB-backed tests **skip cleanly** without docker; unit tests use a fake pinger.
- No domain routes, no auth enforcement, no auto-migrate in `cmd/api`.
- Conventional Commits; **no AI attribution**; commit on `feat/issue-3-api-server` (never `main`).

## File structure

- `backend/internal/config/config.go` (+ `_test.go`) — env config.
- `backend/internal/db/pool.go` — `NewPool`.
- `backend/internal/db/migrate.go` (modify) — accept `postgres://`, add `migrateURL` helper.
- `backend/internal/db/migrate_test.go` (modify) — pass plain URL; `migrateurl_test.go` for the helper.
- `backend/internal/db/pool_test.go` — testcontainers pool ping.
- `backend/internal/web/respond.go` (+ `_test.go`) — JSON helpers.
- `backend/internal/web/middleware.go` (+ `_test.go`) — RequestID/Recoverer/DeviceID.
- `backend/internal/router/router.go` (modify) + `health.go` — `New(Deps)`, DB-backed healthz.
- `backend/internal/router/router_test.go` (modify) — fake pinger, JSON assertions.
- `backend/cmd/api/main.go` (modify), `backend/cmd/lambda/main.go` (modify), `backend/cmd/migrate/main.go` (create).
- `justfile` (create, repo root); `AGENTS.md` (modify); `.env.example` (modify if needed).

---

### Task 1: `internal/config`

**Files:**
- Create: `backend/internal/config/config.go`, `backend/internal/config/config_test.go`

**Interfaces:**
- Produces: `config.Config{ DatabaseURL string; Port string }`; `config.Load() (Config, error)`.

- [ ] **Step 1: Write the failing test**

`backend/internal/config/config_test.go`:

```go
package config_test

import (
	"testing"

	"github.com/stroem/shopping-list/backend/internal/config"
)

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if _, err := config.Load(); err == nil {
		t.Fatal("expected error when DATABASE_URL is unset, got nil")
	}
}

func TestLoadDefaultsPort(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/db")
	t.Setenv("PORT", "")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Port != "8080" {
		t.Fatalf("Port = %q, want 8080", cfg.Port)
	}
	if cfg.DatabaseURL != "postgres://localhost/db" {
		t.Fatalf("DatabaseURL = %q", cfg.DatabaseURL)
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (`undefined: config.Load`)

Run: `cd backend && go test ./internal/config/`

- [ ] **Step 3: Implement `backend/internal/config/config.go`**

```go
// Package config loads server configuration from the environment.
package config

import (
	"errors"
	"os"
)

// Config is the server's runtime configuration, sourced entirely from env vars.
type Config struct {
	DatabaseURL string // required; standard postgres:// URL
	Port        string // listen port, default "8080"
}

// Load reads configuration from the environment. DATABASE_URL is required.
func Load() (Config, error) {
	cfg := Config{
		DatabaseURL: os.Getenv("DATABASE_URL"),
		Port:        os.Getenv("PORT"),
	}
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if cfg.Port == "" {
		cfg.Port = "8080"
	}
	return cfg, nil
}
```

- [ ] **Step 4: Run — expect PASS.** `cd backend && go test ./internal/config/`

- [ ] **Step 5: Commit**

```bash
git add backend/internal/config
git commit -m "feat(config): load DATABASE_URL and PORT from env"
```

---

### Task 2: `web` — JSON responders + middleware

**Files:**
- Create: `backend/internal/web/respond.go`, `backend/internal/web/middleware.go`, `backend/internal/web/web_test.go`

**Interfaces:**
- Produces:
  - `web.JSON(w http.ResponseWriter, status int, v any)`
  - `web.Error(w http.ResponseWriter, status int, msg string)` → body `{"error":"<msg>"}`
  - `web.DeviceID(ctx context.Context) string`
  - `web.DeviceIDMiddleware(next http.Handler) http.Handler` — reads `X-Device-Id` into context.

- [ ] **Step 1: Write the failing test**

`backend/internal/web/web_test.go`:

```go
package web_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stroem/shopping-list/backend/internal/web"
)

func TestErrorWritesJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	web.Error(rec, http.StatusTeapot, "nope")

	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not json: %v", err)
	}
	if body["error"] != "nope" {
		t.Fatalf("error = %q, want nope", body["error"])
	}
}

func TestDeviceIDMiddlewarePopulatesContext(t *testing.T) {
	var got string
	h := web.DeviceIDMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = web.DeviceID(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Device-Id", "dev-123")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got != "dev-123" {
		t.Fatalf("device id = %q, want dev-123", got)
	}
}
```

- [ ] **Step 2: Run — expect FAIL.** `cd backend && go test ./internal/web/`

- [ ] **Step 3: Implement `backend/internal/web/respond.go`**

```go
// Package web holds HTTP helpers shared by every handler: JSON responses and
// request middleware. Domain packages build on these so responses stay uniform.
package web

import (
	"encoding/json"
	"net/http"
)

// JSON writes v as a JSON body with the given status code.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Error writes {"error": msg} as JSON with the given status code.
func Error(w http.ResponseWriter, status int, msg string) {
	JSON(w, status, map[string]string{"error": msg})
}
```

- [ ] **Step 4: Implement `backend/internal/web/middleware.go`**

```go
package web

import (
	"context"
	"net/http"
)

type ctxKey int

const deviceIDKey ctxKey = iota

// DeviceIDMiddleware copies the X-Device-Id request header into the context.
// It does not enforce presence — auth lands with the households work.
func DeviceIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Device-Id")
		ctx := context.WithValue(r.Context(), deviceIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// DeviceID returns the X-Device-Id captured by DeviceIDMiddleware, or "".
func DeviceID(ctx context.Context) string {
	id, _ := ctx.Value(deviceIDKey).(string)
	return id
}
```

- [ ] **Step 5: Run — expect PASS.** `cd backend && go test ./internal/web/`

- [ ] **Step 6: Commit**

```bash
git add backend/internal/web
git commit -m "feat(web): json responders and device-id middleware"
```

---

### Task 3: `db` — `migrateURL` normalization + `NewPool`

**Files:**
- Modify: `backend/internal/db/migrate.go`, `backend/internal/db/migrate_test.go`
- Create: `backend/internal/db/migrateurl_test.go`, `backend/internal/db/pool.go`, `backend/internal/db/pool_test.go`

**Interfaces:**
- Consumes: existing `migrations.FS`.
- Produces: `db.NewPool(ctx, databaseURL) (*pgxpool.Pool, error)`; `Migrate`/`MigrateDown` now accept `postgres://…`.

- [ ] **Step 1: Write the failing helper test**

`backend/internal/db/migrateurl_test.go`:

```go
package db

import "testing"

func TestMigrateURL(t *testing.T) {
	cases := map[string]string{
		"postgres://u:p@h:5432/d?sslmode=disable":   "pgx5://u:p@h:5432/d?sslmode=disable",
		"postgresql://u:p@h:5432/d":                 "pgx5://u:p@h:5432/d",
		"pgx5://u:p@h:5432/d":                       "pgx5://u:p@h:5432/d",
	}
	for in, want := range cases {
		if got := migrateURL(in); got != want {
			t.Errorf("migrateURL(%q) = %q, want %q", in, got, want)
		}
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (`undefined: migrateURL`). `cd backend && go test ./internal/db/ -run TestMigrateURL`

- [ ] **Step 3: Add `migrateURL` and use it in `migrate.go`**

In `backend/internal/db/migrate.go`, add the helper and call it in both `Migrate` and `MigrateDown` (replace the `databaseURL` passed to `migrate.NewWithSourceInstance` with `migrateURL(databaseURL)`). Add `"strings"` to imports.

```go
// migrateURL normalizes a standard postgres URL to the pgx5:// scheme that
// golang-migrate's pgx/v5 driver registers. An already-pgx5:// URL is returned
// unchanged.
func migrateURL(databaseURL string) string {
	switch {
	case strings.HasPrefix(databaseURL, "postgresql://"):
		return "pgx5://" + strings.TrimPrefix(databaseURL, "postgresql://")
	case strings.HasPrefix(databaseURL, "postgres://"):
		return "pgx5://" + strings.TrimPrefix(databaseURL, "postgres://")
	default:
		return databaseURL
	}
}
```

Both runners change their `migrate.NewWithSourceInstance("iofs", src, databaseURL)` call to `migrate.NewWithSourceInstance("iofs", src, migrateURL(databaseURL))`.

- [ ] **Step 4: Simplify `migrate_test.go` to pass the plain URL**

In `backend/internal/db/migrate_test.go`, remove the manual `migrateURL := "pgx5://" + strings.TrimPrefix(...)` line and pass `pgURL` directly to `db.Migrate` / `db.MigrateDown`. Drop the now-unused `strings` import.

```go
	// Up
	if err := db.Migrate(ctx, pgURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	...
	// Down
	if err := db.MigrateDown(pgURL); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
```

- [ ] **Step 5: Write `backend/internal/db/pool.go`**

```go
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool opens a connection pool sized for serverless reuse (low max conns,
// short idle lifetime) so a scale-to-zero Postgres like Neon can suspend cleanly.
// It verifies connectivity with a Ping before returning.
func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.MaxConns = 4
	cfg.MinConns = 0
	cfg.MaxConnIdleTime = 30 * time.Second
	cfg.MaxConnLifetime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}
```

- [ ] **Step 6: Write `backend/internal/db/pool_test.go` (testcontainers, skips without docker)**

```go
package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/stroem/shopping-list/backend/internal/db"
)

func TestNewPoolPings(t *testing.T) {
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("shopping_list"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("5432/tcp").WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Skipf("skipping: cannot start postgres container (docker unavailable?): %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })

	pgURL, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	pool, err := db.NewPool(ctx, pgURL)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
}
```

- [ ] **Step 7: Run the db suite + vet**

Run: `cd backend && go test ./internal/db/ && go vet ./internal/db/`
Expected: `TestMigrateURL` PASS; round-trip + pool tests PASS (docker present) or SKIP.

- [ ] **Step 8: Commit**

```bash
git add backend/internal/db backend/go.mod backend/go.sum
git commit -m "feat(db): add NewPool and normalize migrate URL scheme"
```

---

### Task 4: `router` — `Deps`, middleware stack, DB-backed JSON healthz

**Files:**
- Modify: `backend/internal/router/router.go`, `backend/internal/router/router_test.go`
- Create: `backend/internal/router/health.go`

**Interfaces:**
- Consumes: `web` middleware + responders.
- Produces:
  - `router.Pinger interface { Ping(ctx context.Context) error }`
  - `router.Deps struct { DB Pinger }`
  - `router.New(deps Deps) http.Handler`

- [ ] **Step 1: Rewrite `backend/internal/router/router_test.go`**

```go
package router_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stroem/shopping-list/backend/internal/router"
)

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

func TestHealthzOK(t *testing.T) {
	h := router.New(router.Deps{DB: fakePinger{err: nil}})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["status"] != "ok" {
		t.Fatalf("status field = %v, want ok", body["status"])
	}
}

func TestHealthzDBDown(t *testing.T) {
	h := router.New(router.Deps{DB: fakePinger{err: errors.New("down")}})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["status"] != "unavailable" || body["error"] == nil {
		t.Fatalf("body = %v, want status=unavailable + error", body)
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (`New` signature mismatch / `Deps` undefined). `cd backend && go test ./internal/router/`

- [ ] **Step 3: Rewrite `backend/internal/router/router.go`**

```go
// Package router builds the HTTP routing shared by the local server and the
// Lambda entrypoint, so the two can never diverge.
package router

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/stroem/shopping-list/backend/internal/web"
)

// Pinger is the database dependency the health check needs. *pgxpool.Pool
// satisfies it; tests pass a fake.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Deps are the runtime dependencies the router wires into handlers.
type Deps struct {
	DB Pinger
}

// New returns the application's HTTP handler.
func New(deps Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(web.DeviceIDMiddleware)

	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		web.Error(w, http.StatusNotFound, "not found")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		web.Error(w, http.StatusMethodNotAllowed, "method not allowed")
	})

	r.Get("/healthz", healthz(deps.DB))
	return r
}
```

- [ ] **Step 4: Create `backend/internal/router/health.go`**

```go
package router

import (
	"context"
	"net/http"
	"time"

	"github.com/stroem/shopping-list/backend/internal/web"
)

// healthz reports 200 when the database answers a ping within the timeout,
// 503 otherwise. The server stays up either way.
func healthz(db Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			web.JSON(w, http.StatusServiceUnavailable, map[string]string{
				"status": "unavailable",
				"error":  err.Error(),
			})
			return
		}
		web.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}
```

- [ ] **Step 5: Run — expect PASS.** `cd backend && go test ./internal/router/ && go vet ./internal/router/`

- [ ] **Step 6: Commit**

```bash
git add backend/internal/router
git commit -m "feat(router): db-backed json healthz with middleware stack"
```

---

### Task 5: Entrypoints — `cmd/api`, `cmd/lambda`, `cmd/migrate`

**Files:**
- Modify: `backend/cmd/api/main.go`, `backend/cmd/lambda/main.go`
- Create: `backend/cmd/migrate/main.go`

**Interfaces:**
- Consumes: `config.Load`, `db.NewPool`, `db.Migrate`, `db.MigrateDown`, `router.New`, `router.Deps`.

- [ ] **Step 1: Rewrite `backend/cmd/api/main.go`**

```go
// Command api runs the HTTP backend locally. It is the local-dev baseline and
// shares its router with the Lambda entrypoint. It requires DATABASE_URL.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/stroem/shopping-list/backend/internal/config"
	"github.com/stroem/shopping-list/backend/internal/db"
	"github.com/stroem/shopping-list/backend/internal/router"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pool.Close()

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router.New(router.Deps{DB: pool}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("api listening on %s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
}
```

- [ ] **Step 2: Rewrite `backend/cmd/lambda/main.go`**

```go
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
```

- [ ] **Step 3: Create `backend/cmd/migrate/main.go`**

```go
// Command migrate applies (up) or reverts (down) database migrations. It reads
// DATABASE_URL and is the local/CI migration entrypoint. A richer CLI
// (version/force/steps) is tracked as a follow-up issue.
//
//	go run ./cmd/migrate up
//	go run ./cmd/migrate down
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/stroem/shopping-list/backend/internal/config"
	"github.com/stroem/shopping-list/backend/internal/db"
)

func main() {
	if len(os.Args) != 2 || (os.Args[1] != "up" && os.Args[1] != "down") {
		fmt.Fprintln(os.Stderr, "usage: migrate <up|down>")
		os.Exit(2)
	}
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	switch os.Args[1] {
	case "up":
		if err := db.Migrate(context.Background(), cfg.DatabaseURL); err != nil {
			log.Fatalf("migrate up: %v", err)
		}
		log.Println("migrations applied")
	case "down":
		if err := db.MigrateDown(cfg.DatabaseURL); err != nil {
			log.Fatalf("migrate down: %v", err)
		}
		log.Println("migrations reverted")
	}
}
```

- [ ] **Step 4: Build, vet, full test**

Run: `cd backend && go build ./... && go vet ./... && go test ./...`
Expected: all green (router/web/config unit tests pass; db tests pass with docker or skip).

- [ ] **Step 5: Commit**

```bash
git add backend/cmd
git commit -m "feat(api): wire config, pool, graceful shutdown, and cmd/migrate"
```

---

### Task 6: `justfile`, AGENTS.md, `.env.example`

**Files:**
- Create: `justfile` (repo root)
- Modify: `AGENTS.md`, `.env.example`

**Interfaces:** developer-facing only; nothing imported by code.

- [ ] **Step 1: Create the root `justfile`**

```just
# Shopping List dev tasks. Run `just` to list.
set dotenv-load := true

container := `command -v podman >/dev/null 2>&1 && echo podman || echo docker`

# list recipes
default:
    @just --list

# run the API locally (needs DATABASE_URL; see `just db`)
run:
    cd backend && go run ./cmd/api

# apply migrations
migrate:
    cd backend && go run ./cmd/migrate up

# revert all migrations
migrate-down:
    cd backend && go run ./cmd/migrate down

# start a local postgres for development
db:
    {{container}} run --name shopping-pg -d --rm \
        -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD=postgres \
        -e POSTGRES_DB=shopping_list -p 5432:5432 postgres:16-alpine
    @echo "DATABASE_URL=postgres://postgres:postgres@localhost:5432/shopping_list?sslmode=disable"

# stop the local postgres
db-stop:
    -{{container}} rm -f shopping-pg

# backend tests
backend-test:
    cd backend && go test ./...

# flutter app tests
app-test:
    cd app && flutter test

# all tests
test: backend-test app-test

# run the flutter app in chrome
app-run:
    cd app && flutter run -d chrome

# build everything
build:
    cd backend && go build ./...
    cd app && flutter build web

# vet + tidy backend
vet:
    cd backend && go vet ./...

tidy:
    cd backend && go mod tidy
```

- [ ] **Step 2: Verify `just` parses and lists**

Run: `just --list`
Expected: the recipes above are listed (if `just` is not installed, note it and skip — the recipes are still correct).

- [ ] **Step 3: Update `.env.example`** (ensure the local value matches `just db`)

Ensure `backend`'s `DATABASE_URL` example reads:

```
DATABASE_URL=postgres://postgres:postgres@localhost:5432/shopping_list?sslmode=disable
```

(If already present from #1, leave as-is.)

- [ ] **Step 4: Update `AGENTS.md` Build/run/test section**

In `AGENTS.md`, under "Build, run, test", add a short note: a root `justfile`
exists (`just` to list); `cmd/api` **requires `DATABASE_URL`**; `just db` starts a
local Postgres and prints the URL; `just migrate` applies migrations. Keep the
existing plain `go`/`flutter` commands too. Exact insertion (after the backend
bullet list):

```markdown
A root **`justfile`** wraps these: `just run` (API), `just db` / `just db-stop`
(local Postgres), `just migrate`, `just test` (Go + Flutter), `just app-run`.
`cmd/api` **requires `DATABASE_URL`** (use `just db` to get one locally).
```

- [ ] **Step 5: Commit**

```bash
git add justfile AGENTS.md .env.example
git commit -m "chore: add root justfile and document local dev workflow"
```

---

## Self-Review

**Spec coverage:**
- `config` (required DATABASE_URL, default port) → Task 1. ✓
- `web` middleware + JSON helpers (RequestID/Recoverer via chi in router; DeviceID + responders in web) → Task 2 + Task 4. ✓
- `db.NewPool` serverless-sized + `migrateURL` single-format refactor → Task 3. ✓
- `router.New(Deps)` + DB-backed JSON healthz → Task 4. ✓
- `cmd/api` (fatal on missing URL, graceful shutdown), `cmd/lambda` (new signature), `cmd/migrate` → Task 5. ✓
- `justfile`, AGENTS.md note, `.env.example` → Task 6. ✓
- Tests: fake pinger (router), config, web, migrateURL pure unit, NewPool testcontainers skip-clean → Tasks 1–4. ✓

**Placeholder scan:** No TBD/TODO; every code block is complete. `cmd/migrate` doc-comment notes the richer CLI is a follow-up (intentional, not a gap). ✓

**Type consistency:** `router.Pinger.Ping(ctx) error` matches `*pgxpool.Pool.Ping` and `fakePinger`. `router.Deps{DB: pool}` used identically in `cmd/api`/`cmd/lambda`/tests. `web.JSON`/`web.Error`/`web.DeviceID`/`web.DeviceIDMiddleware` names match across web, router, and health. `db.NewPool`/`db.Migrate`/`db.MigrateDown`/`migrateURL` consistent across db, cmd/migrate, and tests. ✓
