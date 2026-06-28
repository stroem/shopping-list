# Households & OIDC Identity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Backend identity foundation: verify Google/OIDC ID tokens (configurable issuer), key a person by `(issuer, subject)`, and let people create/join a household by invite code — with cross-household access returning 404.

**Architecture:** A new `internal/auth` package (token verification + optional-auth middleware + user auto-create), `web.Principal` context plumbing, an `internal/households` store, and router endpoints under `/v1`. The `/v1/suggest` seam reads the authenticated household from context. OIDC is hermetically tested with a fake verifier plus an in-process `mockoidc` provider.

**Tech Stack:** Go 1.26 · go-chi/chi v5 · jackc/pgx/v5 · `github.com/coreos/go-oidc/v3` · `github.com/oauth2-proxy/mockoidc` (test) · testcontainers-go.

## Global Constraints

- Go module `github.com/stroem/shopping-list/backend`; `go 1.26`.
- Person identity = OIDC `(issuer, subject)`; `users` auto-created on first authenticated request. Households shared by an unguessable `invite_code`.
- Endpoints under `/v1`: `POST /v1/households` (→ invite_code), `POST /v1/households/join` (`{invite_code}`), `GET /v1/households/{id}` (404 unless caller's). All require auth; `/v1/suggest` stays open.
- Auth is optional-middleware: absent token → no principal; invalid token → **401**; cross-household / unknown code → **404** (no existence leak).
- `OIDC_ISSUER` default `https://accounts.google.com`; `OIDC_AUDIENCE` optional (empty ⇒ denyVerifier, server still boots). Local-dev baseline (`go run ./cmd/api` boots, `/healthz` ok) must hold.
- DB-backed tests skip cleanly without docker. Conventional Commits; **no AI attribution**; commit on `feat/issue-8-households-identity` only.

## File structure

- `backend/migrations/0003_oidc_identity.{up,down}.sql`
- `backend/internal/config/config.go` (modify) + `../.env.example` (modify)
- `backend/internal/web/principal.go` (+ `principal_test.go`)
- `backend/internal/auth/verifier.go`, `store.go`, `middleware.go` (+ tests), `oidc.go` (+ `oidc_test.go`)
- `backend/internal/households/store.go` (+ `store_test.go`)
- `backend/internal/router/households.go` (+ `households_handler_test.go`), `router.go` (modify)
- `backend/internal/suggest/suggest.go` (modify) + `suggest_principal_test.go`
- `backend/cmd/api/main.go`, `backend/cmd/lambda/main.go` (modify)
- `backend/internal/auth/e2e_test.go` (mockoidc + testcontainers)
- `AGENTS.md` (modify)

---

### Task 1: Migration `0003` + OIDC config + .env.example

**Files:**
- Create: `backend/migrations/0003_oidc_identity.up.sql`, `backend/migrations/0003_oidc_identity.down.sql`
- Modify: `backend/internal/config/config.go`, `.env.example`

**Interfaces:**
- Produces: `users.{issuer,subject,email}` + `UNIQUE(issuer,subject)`, nullable `device_id`; `households.invite_code`. `Config.OIDCIssuer`, `Config.OIDCAudience`.

- [ ] **Step 1: Write `backend/migrations/0003_oidc_identity.up.sql`**

```sql
-- A user is now a person keyed by their OIDC identity (issuer, subject).
-- device_id is no longer the identity (kept nullable for future device/sync use).
ALTER TABLE users
    ADD COLUMN issuer  text,
    ADD COLUMN subject text,
    ADD COLUMN email   text,
    ALTER COLUMN device_id DROP NOT NULL;
CREATE UNIQUE INDEX users_issuer_subject ON users (issuer, subject);

-- Households are shared via an unguessable invite code (carried by the link).
ALTER TABLE households
    ADD COLUMN invite_code text NOT NULL DEFAULT gen_random_uuid()::text;
CREATE UNIQUE INDEX households_invite_code ON households (invite_code);
```

- [ ] **Step 2: Write `backend/migrations/0003_oidc_identity.down.sql`**

```sql
DROP INDEX IF EXISTS households_invite_code;
ALTER TABLE households DROP COLUMN IF EXISTS invite_code;

DROP INDEX IF EXISTS users_issuer_subject;
ALTER TABLE users
    DROP COLUMN IF EXISTS email,
    DROP COLUMN IF EXISTS subject,
    DROP COLUMN IF EXISTS issuer,
    ALTER COLUMN device_id SET NOT NULL;
```

- [ ] **Step 3: Extend `backend/internal/config/config.go`**

Add two fields and load them (neither required):

```go
// Config is the server's runtime configuration, sourced entirely from env vars.
type Config struct {
	DatabaseURL  string // required; standard postgres:// URL
	Port         string // listen port, default "8080"
	OIDCIssuer   string // OIDC issuer, default https://accounts.google.com
	OIDCAudience string // OIDC audience (Google client id); empty ⇒ auth disabled
}
```

and in `Load`, before the final `return`:

```go
	cfg.OIDCIssuer = os.Getenv("OIDC_ISSUER")
	if cfg.OIDCIssuer == "" {
		cfg.OIDCIssuer = "https://accounts.google.com"
	}
	cfg.OIDCAudience = os.Getenv("OIDC_AUDIENCE")
```

- [ ] **Step 4: Document in `.env.example`** (append)

```
# OIDC sign-in (Google). Issuer defaults to https://accounts.google.com.
# OIDC_AUDIENCE is your Google OAuth client id; leave empty to disable auth
# locally (the server still boots; auth-only endpoints return 401).
OIDC_ISSUER=https://accounts.google.com
OIDC_AUDIENCE=
```

- [ ] **Step 5: Verify migration round-trip + config build**

Run: `cd backend && go test ./internal/db/ -run TestMigrateUpDownRoundTrip && go test ./internal/config/ && go build ./...`
Expected: PASS (or SKIP without docker); config + build green.

- [ ] **Step 6: Commit**

```bash
git add backend/migrations/0003_oidc_identity.up.sql backend/migrations/0003_oidc_identity.down.sql backend/internal/config/config.go .env.example
git commit -m "feat(migrations): oidc identity columns + invite code; config"
```

---

### Task 2: `web.Principal` + context helpers (TDD)

**Files:**
- Create: `backend/internal/web/principal.go`, `backend/internal/web/principal_test.go`

**Interfaces:**
- Produces: `type Principal struct { UserID string; HouseholdID *string; Email string }`,
  `WithPrincipal(ctx, Principal) context.Context`, `PrincipalFrom(ctx) (Principal, bool)`,
  `HouseholdID(ctx) (string, bool)`.

- [ ] **Step 1: Write the failing test** — `backend/internal/web/principal_test.go`

```go
package web

import (
	"context"
	"testing"
)

func TestPrincipalRoundTrip(t *testing.T) {
	ctx := context.Background()
	if _, ok := PrincipalFrom(ctx); ok {
		t.Fatal("empty ctx should have no principal")
	}
	if _, ok := HouseholdID(ctx); ok {
		t.Fatal("empty ctx should have no household")
	}

	hh := "11111111-1111-1111-1111-111111111111"
	ctx = WithPrincipal(ctx, Principal{UserID: "u1", HouseholdID: &hh, Email: "a@b.c"})

	p, ok := PrincipalFrom(ctx)
	if !ok || p.UserID != "u1" || p.Email != "a@b.c" || p.HouseholdID == nil || *p.HouseholdID != hh {
		t.Fatalf("principal = %+v ok=%v", p, ok)
	}
	if id, ok := HouseholdID(ctx); !ok || id != hh {
		t.Fatalf("household = %q ok=%v", id, ok)
	}

	// A principal with no household → HouseholdID false.
	ctx2 := WithPrincipal(context.Background(), Principal{UserID: "u2"})
	if _, ok := HouseholdID(ctx2); ok {
		t.Fatal("no-household principal should give HouseholdID ok=false")
	}
}
```

- [ ] **Step 2: Run — expect FAIL.** `cd backend && go test ./internal/web/ -run TestPrincipal`

- [ ] **Step 3: Implement `backend/internal/web/principal.go`**

```go
package web

import "context"

// Principal is the authenticated caller, set by the auth middleware.
type Principal struct {
	UserID      string
	HouseholdID *string
	Email       string
}

type principalKey struct{}

// WithPrincipal returns a context carrying p.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the principal set by the auth middleware, if any.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// HouseholdID returns the caller's household id, or ("", false) when there is no
// principal or the principal has no household yet.
func HouseholdID(ctx context.Context) (string, bool) {
	p, ok := PrincipalFrom(ctx)
	if !ok || p.HouseholdID == nil {
		return "", false
	}
	return *p.HouseholdID, true
}
```

- [ ] **Step 4: Run — expect PASS.** `cd backend && go test ./internal/web/`

- [ ] **Step 5: Commit**

```bash
git add backend/internal/web/principal.go backend/internal/web/principal_test.go
git commit -m "feat(web): principal context plumbing"
```

---

### Task 3: `auth` verifier interface + user store (TDD, testcontainers)

**Files:**
- Create: `backend/internal/auth/verifier.go`, `backend/internal/auth/store.go`, `backend/internal/auth/store_test.go`

**Interfaces:**
- Produces:
  - `type Claims struct { Subject, Issuer, Email string }`
  - `type TokenVerifier interface { Verify(ctx context.Context, rawToken string) (Claims, error) }`
  - `denyVerifier` (always errors)
  - `type UserStore interface { Upsert(ctx, issuer, subject, email string) (userID string, householdID *string, err error) }`
  - `func NewUserStore(db *pgxpool.Pool) *PgUserStore`

- [ ] **Step 1: Write `backend/internal/auth/verifier.go`**

```go
// Package auth verifies OIDC ID tokens and resolves the authenticated person.
package auth

import (
	"context"
	"errors"
)

// Claims are the verified identity fields the app needs.
type Claims struct {
	Subject string
	Issuer  string
	Email   string
}

// TokenVerifier verifies a raw bearer token and returns its claims.
type TokenVerifier interface {
	Verify(ctx context.Context, rawToken string) (Claims, error)
}

// denyVerifier rejects every token; used when no OIDC audience is configured so
// the server can still boot (auth-only endpoints return 401).
type denyVerifier struct{}

// ErrAuthDisabled is returned by the deny verifier.
var ErrAuthDisabled = errors.New("auth not configured")

func (denyVerifier) Verify(context.Context, string) (Claims, error) {
	return Claims{}, ErrAuthDisabled
}

// NewDenyVerifier returns a verifier that rejects all tokens.
func NewDenyVerifier() TokenVerifier { return denyVerifier{} }
```

- [ ] **Step 2: Write the failing store test** — `backend/internal/auth/store_test.go`

```go
package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/stroem/shopping-list/backend/internal/auth"
	"github.com/stroem/shopping-list/backend/internal/db"
)

func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
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
	if err := db.Migrate(ctx, pgURL); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxpool.New(ctx, pgURL)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestUserStoreUpsertAutoCreates(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	s := auth.NewUserStore(pool)

	uid, hh, err := s.Upsert(ctx, "https://accounts.google.com", "sub-1", "a@example.com")
	if err != nil || uid == "" {
		t.Fatalf("first upsert: uid=%q err=%v", uid, err)
	}
	if hh != nil {
		t.Fatalf("new user should have no household, got %v", *hh)
	}

	// Same identity → same row (id stable), email updated.
	uid2, _, err := s.Upsert(ctx, "https://accounts.google.com", "sub-1", "new@example.com")
	if err != nil || uid2 != uid {
		t.Fatalf("second upsert: uid2=%q (want %q) err=%v", uid2, uid, err)
	}
	var count int
	var email string
	if err := pool.QueryRow(ctx,
		`SELECT count(*) OVER (), email FROM users WHERE issuer=$1 AND subject=$2`,
		"https://accounts.google.com", "sub-1").Scan(&count, &email); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if count != 1 || email != "new@example.com" {
		t.Fatalf("after re-upsert: count=%d email=%q", count, email)
	}
}
```

- [ ] **Step 3: Run — expect FAIL** (`undefined: auth.NewUserStore`). `cd backend && go test ./internal/auth/`

- [ ] **Step 4: Implement `backend/internal/auth/store.go`**

```go
package auth

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// UserStore upserts the authenticated person and returns their household (if any).
type UserStore interface {
	Upsert(ctx context.Context, issuer, subject, email string) (userID string, householdID *string, err error)
}

// PgUserStore is the pgx-backed UserStore.
type PgUserStore struct{ db *pgxpool.Pool }

// NewUserStore builds a PgUserStore.
func NewUserStore(db *pgxpool.Pool) *PgUserStore { return &PgUserStore{db: db} }

const upsertUserSQL = `
INSERT INTO users (issuer, subject, email)
VALUES ($1, $2, $3)
ON CONFLICT (issuer, subject)
DO UPDATE SET email = EXCLUDED.email, updated_at = now()
RETURNING id::text, household_id::text`

// Upsert auto-creates the user on first sight, keyed by (issuer, subject).
func (s *PgUserStore) Upsert(ctx context.Context, issuer, subject, email string) (string, *string, error) {
	var id string
	var household *string
	err := s.db.QueryRow(ctx, upsertUserSQL, issuer, subject, email).Scan(&id, &household)
	if err != nil {
		return "", nil, fmt.Errorf("upsert user: %w", err)
	}
	return id, household, nil
}
```

- [ ] **Step 5: Run — expect PASS (or SKIP).** `cd backend && go test ./internal/auth/ && go vet ./internal/auth/`

- [ ] **Step 6: Commit**

```bash
git add backend/internal/auth/verifier.go backend/internal/auth/store.go backend/internal/auth/store_test.go
git commit -m "feat(auth): token verifier interface and user upsert store"
```

---

### Task 4: Auth middleware + RequireAuth (TDD, fakes)

**Files:**
- Create: `backend/internal/auth/middleware.go`, `backend/internal/auth/middleware_test.go`

**Interfaces:**
- Consumes: `TokenVerifier`, `UserStore`, `web.Principal`, `web.WithPrincipal`, `web.Error`.
- Produces: `func Middleware(v TokenVerifier, store UserStore) func(http.Handler) http.Handler`, `func RequireAuth(next http.Handler) http.Handler`.

- [ ] **Step 1: Write the failing test** — `backend/internal/auth/middleware_test.go`

```go
package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stroem/shopping-list/backend/internal/auth"
	"github.com/stroem/shopping-list/backend/internal/web"
)

type fakeVerifier struct {
	claims auth.Claims
	err    error
}

func (f fakeVerifier) Verify(context.Context, string) (auth.Claims, error) {
	return f.claims, f.err
}

type fakeStore struct {
	uid       string
	household *string
	gotIss    string
	gotSub    string
}

func (f *fakeStore) Upsert(_ context.Context, issuer, subject, _ string) (string, *string, error) {
	f.gotIss, f.gotSub = issuer, subject
	return f.uid, f.household, nil
}

func serve(mw func(http.Handler) http.Handler, inner http.Handler, authz string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)
	return rec
}

func TestMiddleware_NoHeader_NoPrincipal(t *testing.T) {
	store := &fakeStore{uid: "u1"}
	seen := false
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if _, ok := web.PrincipalFrom(r.Context()); ok {
			t.Error("expected no principal")
		}
		seen = true
	})
	rec := serve(auth.Middleware(fakeVerifier{}, store), inner, "")
	if !seen || rec.Code != http.StatusOK {
		t.Fatalf("seen=%v code=%d", seen, rec.Code)
	}
}

func TestMiddleware_InvalidToken_401(t *testing.T) {
	mw := auth.Middleware(fakeVerifier{err: errors.New("bad")}, &fakeStore{})
	inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("inner must not run") })
	rec := serve(mw, inner, "Bearer xyz")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rec.Code)
	}
}

func TestMiddleware_ValidToken_SetsPrincipalAndUpserts(t *testing.T) {
	hh := "h-1"
	store := &fakeStore{uid: "u-9", household: &hh}
	v := fakeVerifier{claims: auth.Claims{Subject: "sub-9", Issuer: "iss-9", Email: "z@x.y"}}
	var got web.Principal
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = web.PrincipalFrom(r.Context())
	})
	rec := serve(auth.Middleware(v, store), inner, "Bearer good")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	if got.UserID != "u-9" || got.Email != "z@x.y" || got.HouseholdID == nil || *got.HouseholdID != "h-1" {
		t.Fatalf("principal = %+v", got)
	}
	if store.gotIss != "iss-9" || store.gotSub != "sub-9" {
		t.Fatalf("store got iss/sub = %q/%q", store.gotIss, store.gotSub)
	}
}

func TestRequireAuth(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	// No principal → 401.
	rec := httptest.NewRecorder()
	auth.RequireAuth(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no principal: code = %d, want 401", rec.Code)
	}

	// With principal → passes.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(web.WithPrincipal(req.Context(), web.Principal{UserID: "u1"}))
	rec = httptest.NewRecorder()
	auth.RequireAuth(inner).ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("with principal: code = %d, want 204", rec.Code)
	}
}
```

- [ ] **Step 2: Run — expect FAIL.** `cd backend && go test ./internal/auth/ -run 'Middleware|RequireAuth'`

- [ ] **Step 3: Implement `backend/internal/auth/middleware.go`**

```go
package auth

import (
	"net/http"
	"strings"

	"github.com/stroem/shopping-list/backend/internal/web"
)

// Middleware is optional auth: when an "Authorization: Bearer <token>" header is
// present it verifies the token, upserts the user, and puts a web.Principal in
// the context. An invalid token is a 401; an absent header passes through with no
// principal (open endpoints still work).
func Middleware(v TokenVerifier, store UserStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := bearer(r)
			if raw == "" {
				next.ServeHTTP(w, r)
				return
			}
			claims, err := v.Verify(r.Context(), raw)
			if err != nil {
				web.Error(w, http.StatusUnauthorized, "invalid token")
				return
			}
			userID, household, err := store.Upsert(r.Context(), claims.Issuer, claims.Subject, claims.Email)
			if err != nil {
				web.Error(w, http.StatusInternalServerError, "auth store failed")
				return
			}
			ctx := web.WithPrincipal(r.Context(), web.Principal{
				UserID: userID, HouseholdID: household, Email: claims.Email,
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAuth rejects requests without a principal (401). Use it to guard
// endpoints that need an authenticated caller.
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := web.PrincipalFrom(r.Context()); !ok {
			web.Error(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const p = "Bearer "
	if len(h) > len(p) && strings.EqualFold(h[:len(p)], p) {
		return strings.TrimSpace(h[len(p):])
	}
	return ""
}
```

- [ ] **Step 4: Run — expect PASS.** `cd backend && go test ./internal/auth/ && go vet ./internal/auth/`

- [ ] **Step 5: Commit**

```bash
git add backend/internal/auth/middleware.go backend/internal/auth/middleware_test.go
git commit -m "feat(auth): optional-auth middleware and RequireAuth guard"
```

---

### Task 5: `OIDCVerifier` with go-oidc + mockoidc test

**Files:**
- Create: `backend/internal/auth/oidc.go`, `backend/internal/auth/oidc_test.go`

**Interfaces:**
- Produces: `func NewOIDCVerifier(ctx, issuer, audience string) (*OIDCVerifier, error)`; `OIDCVerifier` implements `TokenVerifier`.

- [ ] **Step 1: Add the dependencies**

```bash
cd backend
go get github.com/coreos/go-oidc/v3/oidc@latest
go get github.com/oauth2-proxy/mockoidc@latest
go mod tidy
```

- [ ] **Step 2: Implement `backend/internal/auth/oidc.go`**

```go
package auth

import (
	"context"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
)

// OIDCVerifier verifies Google/OIDC ID tokens against a configured issuer and
// audience using OIDC discovery (JWKS fetched and cached by go-oidc).
type OIDCVerifier struct {
	verifier *oidc.IDTokenVerifier
}

// NewOIDCVerifier discovers the issuer's metadata and builds a verifier bound to
// the given audience (the OAuth client id).
func NewOIDCVerifier(ctx context.Context, issuer, audience string) (*OIDCVerifier, error) {
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery for %s: %w", issuer, err)
	}
	return &OIDCVerifier{verifier: provider.Verifier(&oidc.Config{ClientID: audience})}, nil
}

// Verify validates the token's signature, issuer, audience and expiry, then
// returns its identity claims.
func (o *OIDCVerifier) Verify(ctx context.Context, rawToken string) (Claims, error) {
	idToken, err := o.verifier.Verify(ctx, rawToken)
	if err != nil {
		return Claims{}, fmt.Errorf("verify id token: %w", err)
	}
	var c struct {
		Email string `json:"email"`
	}
	_ = idToken.Claims(&c)
	return Claims{Subject: idToken.Subject, Issuer: idToken.Issuer, Email: c.Email}, nil
}
```

- [ ] **Step 3: Write the hermetic verifier test** — `backend/internal/auth/oidc_test.go`

```go
package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/oauth2-proxy/mockoidc"

	"github.com/stroem/shopping-list/backend/internal/auth"
)

func TestOIDCVerifier_VerifiesMintedToken(t *testing.T) {
	m, err := mockoidc.Run()
	if err != nil {
		t.Fatalf("mockoidc: %v", err)
	}
	defer m.Shutdown()

	ctx := context.Background()
	v, err := auth.NewOIDCVerifier(ctx, m.Issuer(), m.Config().ClientID)
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}

	now := time.Now()
	tok, err := m.Keypair.SignJWT(jwt.MapClaims{
		"iss":   m.Issuer(),
		"aud":   m.Config().ClientID,
		"sub":   "google-sub-123",
		"email": "person@example.com",
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	claims, err := v.Verify(ctx, tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.Subject != "google-sub-123" || claims.Email != "person@example.com" || claims.Issuer != m.Issuer() {
		t.Fatalf("claims = %+v", claims)
	}

	// Wrong audience must fail.
	bad, _ := m.Keypair.SignJWT(jwt.MapClaims{
		"iss": m.Issuer(), "aud": "someone-else",
		"sub": "x", "exp": now.Add(time.Hour).Unix(),
	})
	if _, err := v.Verify(ctx, bad); err == nil {
		t.Fatal("expected wrong-audience token to fail verification")
	}
}
```

Note: `mockoidc` runs in-process (no docker). If its exact minting API differs (`m.Keypair.SignJWT`, `m.Issuer()`, `m.Config().ClientID`), adjust to the installed version's API — the shape (mint a token signed by the mock's key, verify against its issuer) is what matters.

- [ ] **Step 4: Run — expect PASS.** `cd backend && go test ./internal/auth/ -run TestOIDCVerifier -v`

- [ ] **Step 5: Commit**

```bash
git add backend/internal/auth/oidc.go backend/internal/auth/oidc_test.go backend/go.mod backend/go.sum
git commit -m "feat(auth): OIDC ID-token verifier (configurable issuer)"
```

---

### Task 6: `households.Store` (TDD, testcontainers)

**Files:**
- Create: `backend/internal/households/store.go`, `backend/internal/households/store_test.go`

**Interfaces:**
- Produces:
  - `type Household struct { ID string; Name *string; InviteCode string }`
  - `var ErrNotFound = errors.New("household not found")`
  - `func NewStore(db *pgxpool.Pool) *Store`
  - `Create(ctx, userID string, name *string) (Household, error)`, `JoinByCode(ctx, userID, code string) (Household, error)`, `Get(ctx, id string) (Household, error)`

- [ ] **Step 1: Write the failing test** — `backend/internal/households/store_test.go`

```go
package households_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/stroem/shopping-list/backend/internal/db"
	"github.com/stroem/shopping-list/backend/internal/households"
)

func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
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
	if err := db.Migrate(ctx, pgURL); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxpool.New(ctx, pgURL)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func mkUser(t *testing.T, pool *pgxpool.Pool, sub string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (issuer, subject) VALUES ('iss', $1) RETURNING id::text`, sub,
	).Scan(&id); err != nil {
		t.Fatalf("mkUser: %v", err)
	}
	return id
}

func TestHouseholdCreateJoinGet(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	s := households.NewStore(pool)

	userA := mkUser(t, pool, "subA")
	userB := mkUser(t, pool, "subB")

	name := "Home"
	hh, err := s.Create(ctx, userA, &name)
	if err != nil || hh.ID == "" || hh.InviteCode == "" {
		t.Fatalf("create: %+v err=%v", hh, err)
	}
	// userA is now in the household.
	var aHH string
	if err := pool.QueryRow(ctx, `SELECT household_id::text FROM users WHERE id=$1::uuid`, userA).Scan(&aHH); err != nil || aHH != hh.ID {
		t.Fatalf("userA household = %q want %q err=%v", aHH, hh.ID, err)
	}

	// userB joins by code → same household.
	joined, err := s.JoinByCode(ctx, userB, hh.InviteCode)
	if err != nil || joined.ID != hh.ID {
		t.Fatalf("join: %+v err=%v", joined, err)
	}

	// Bad code → ErrNotFound.
	if _, err := s.JoinByCode(ctx, userB, "nope-nope"); !errors.Is(err, households.ErrNotFound) {
		t.Fatalf("join bad code err = %v, want ErrNotFound", err)
	}

	// Get existing / missing.
	if g, err := s.Get(ctx, hh.ID); err != nil || g.InviteCode != hh.InviteCode {
		t.Fatalf("get: %+v err=%v", g, err)
	}
	if _, err := s.Get(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, households.ErrNotFound) {
		t.Fatalf("get missing err = %v, want ErrNotFound", err)
	}
}
```

- [ ] **Step 2: Run — expect FAIL.** `cd backend && go test ./internal/households/`

- [ ] **Step 3: Implement `backend/internal/households/store.go`**

```go
// Package households manages household creation and invite-code membership.
package households

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound means no household matched (missing id or unknown invite code).
var ErrNotFound = errors.New("household not found")

// Household is a shared list owner.
type Household struct {
	ID         string
	Name       *string
	InviteCode string
}

// Store persists households.
type Store struct{ db *pgxpool.Pool }

// NewStore builds a Store.
func NewStore(db *pgxpool.Pool) *Store { return &Store{db: db} }

// Create makes a household and puts the caller in it, atomically.
func (s *Store) Create(ctx context.Context, userID string, name *string) (Household, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Household{}, err
	}
	defer tx.Rollback(ctx)

	var h Household
	if err := tx.QueryRow(ctx,
		`INSERT INTO households (name) VALUES ($1) RETURNING id::text, name, invite_code`, name,
	).Scan(&h.ID, &h.Name, &h.InviteCode); err != nil {
		return Household{}, fmt.Errorf("insert household: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE users SET household_id = $1::uuid, updated_at = now() WHERE id = $2::uuid`, h.ID, userID,
	); err != nil {
		return Household{}, fmt.Errorf("set household: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Household{}, err
	}
	return h, nil
}

// JoinByCode associates the caller with the household for invite code, if any.
func (s *Store) JoinByCode(ctx context.Context, userID, code string) (Household, error) {
	var h Household
	err := s.db.QueryRow(ctx,
		`SELECT id::text, name, invite_code FROM households WHERE invite_code = $1 AND deleted_at IS NULL`, code,
	).Scan(&h.ID, &h.Name, &h.InviteCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return Household{}, ErrNotFound
	}
	if err != nil {
		return Household{}, fmt.Errorf("find household: %w", err)
	}
	if _, err := s.db.Exec(ctx,
		`UPDATE users SET household_id = $1::uuid, updated_at = now() WHERE id = $2::uuid`, h.ID, userID,
	); err != nil {
		return Household{}, fmt.Errorf("join household: %w", err)
	}
	return h, nil
}

// Get returns the household by id, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id string) (Household, error) {
	var h Household
	err := s.db.QueryRow(ctx,
		`SELECT id::text, name, invite_code FROM households WHERE id = $1::uuid AND deleted_at IS NULL`, id,
	).Scan(&h.ID, &h.Name, &h.InviteCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return Household{}, ErrNotFound
	}
	if err != nil {
		return Household{}, fmt.Errorf("get household: %w", err)
	}
	return h, nil
}
```

- [ ] **Step 4: Run — expect PASS (or SKIP).** `cd backend && go test ./internal/households/ && go vet ./internal/households/`

- [ ] **Step 5: Commit**

```bash
git add backend/internal/households/store.go backend/internal/households/store_test.go
git commit -m "feat(households): create, join-by-code, get store"
```

---

### Task 7: Household HTTP handlers + router wiring (TDD, fakes)

**Files:**
- Create: `backend/internal/router/households.go`, `backend/internal/router/households_handler_test.go`
- Modify: `backend/internal/router/router.go`

**Interfaces:**
- Consumes: `households.Household`, `households.ErrNotFound`, `web.PrincipalFrom`, `auth.RequireAuth`, `web.JSON`/`web.Error`.
- Produces: `type HouseholdStore interface { Create(...); JoinByCode(...); Get(...) }`; `Deps.AuthMiddleware`, `Deps.Households`; routes.

- [ ] **Step 1: Write the failing handler test** — `backend/internal/router/households_handler_test.go`

```go
package router_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stroem/shopping-list/backend/internal/auth"
	"github.com/stroem/shopping-list/backend/internal/households"
	"github.com/stroem/shopping-list/backend/internal/router"
	"github.com/stroem/shopping-list/backend/internal/web"
)

type fakeHouseholds struct {
	created  households.Household
	joinErr  error
	getErr   error
	got      households.Household
}

func (f *fakeHouseholds) Create(_ context.Context, _ string, _ *string) (households.Household, error) {
	return f.created, nil
}
func (f *fakeHouseholds) JoinByCode(_ context.Context, _, _ string) (households.Household, error) {
	return f.got, f.joinErr
}
func (f *fakeHouseholds) Get(_ context.Context, _ string) (households.Household, error) {
	return f.got, f.getErr
}

// principalMW injects a fixed principal so RequireAuth passes in tests.
func principalMW(p *web.Principal) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if p != nil {
				r = r.WithContext(web.WithPrincipal(r.Context(), *p))
			}
			next.ServeHTTP(w, r)
		})
	}
}

func newHouseholdRouter(p *web.Principal, store router.HouseholdStore) http.Handler {
	return router.New(router.Deps{
		DB:             fakePinger{},
		AuthMiddleware: principalMW(p),
		Households:     store,
	})
}

func TestCreateHousehold_201WithInviteCode(t *testing.T) {
	hid := "h-1"
	p := &web.Principal{UserID: "u-1"}
	store := &fakeHouseholds{created: households.Household{ID: hid, InviteCode: "code-xyz"}}
	h := newHouseholdRouter(p, store)

	req := httptest.NewRequest(http.MethodPost, "/v1/households", strings.NewReader(`{"name":"Home"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("code = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	var got households.Household
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.InviteCode != "code-xyz" || got.ID != hid {
		t.Fatalf("body = %s", rec.Body)
	}
}

func TestCreateHousehold_NoAuth_401(t *testing.T) {
	store := &fakeHouseholds{}
	h := newHouseholdRouter(nil, store) // no principal injected
	req := httptest.NewRequest(http.MethodPost, "/v1/households", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rec.Code)
	}
}

func TestJoinHousehold_UnknownCode_404(t *testing.T) {
	p := &web.Principal{UserID: "u-1"}
	store := &fakeHouseholds{joinErr: households.ErrNotFound}
	h := newHouseholdRouter(p, store)
	req := httptest.NewRequest(http.MethodPost, "/v1/households/join", strings.NewReader(`{"invite_code":"nope"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rec.Code)
	}
}

func TestGetHousehold_NotMine_404(t *testing.T) {
	mine := "h-mine"
	p := &web.Principal{UserID: "u-1", HouseholdID: &mine}
	store := &fakeHouseholds{got: households.Household{ID: "h-other", InviteCode: "c"}}
	h := newHouseholdRouter(p, store)
	req := httptest.NewRequest(http.MethodGet, "/v1/households/h-other", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("not-mine code = %d, want 404", rec.Code)
	}
}

func TestGetHousehold_Mine_200(t *testing.T) {
	mine := "h-mine"
	p := &web.Principal{UserID: "u-1", HouseholdID: &mine}
	store := &fakeHouseholds{got: households.Household{ID: mine, InviteCode: "c"}}
	h := newHouseholdRouter(p, store)
	req := httptest.NewRequest(http.MethodGet, "/v1/households/"+mine, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mine code = %d, want 200", rec.Code)
	}
	_ = auth.ErrAuthDisabled // keep auth import used across the test file
}
```

- [ ] **Step 2: Run — expect FAIL** (unknown `Deps` fields / routes). `cd backend && go test ./internal/router/`

- [ ] **Step 3: Implement `backend/internal/router/households.go`**

```go
package router

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/stroem/shopping-list/backend/internal/households"
	"github.com/stroem/shopping-list/backend/internal/web"
)

// HouseholdStore is the household persistence the handlers need; *households.Store
// satisfies it, tests pass a fake.
type HouseholdStore interface {
	Create(ctx context.Context, userID string, name *string) (households.Household, error)
	JoinByCode(ctx context.Context, userID, code string) (households.Household, error)
	Get(ctx context.Context, id string) (households.Household, error)
}

func createHousehold(store HouseholdStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, _ := web.PrincipalFrom(r.Context()) // RequireAuth guarantees presence
		var body struct {
			Name *string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		h, err := store.Create(r.Context(), p.UserID, body.Name)
		if err != nil {
			web.Error(w, http.StatusInternalServerError, "create failed")
			return
		}
		web.JSON(w, http.StatusCreated, h)
	}
}

func joinHousehold(store HouseholdStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, _ := web.PrincipalFrom(r.Context())
		var body struct {
			InviteCode string `json:"invite_code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.InviteCode == "" {
			web.Error(w, http.StatusBadRequest, "invite_code required")
			return
		}
		h, err := store.JoinByCode(r.Context(), p.UserID, body.InviteCode)
		if errors.Is(err, households.ErrNotFound) {
			web.Error(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			web.Error(w, http.StatusInternalServerError, "join failed")
			return
		}
		web.JSON(w, http.StatusOK, h)
	}
}

func getHousehold(store HouseholdStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		mine, ok := web.HouseholdID(r.Context())
		// Not yours (or you have none) → 404, no existence leak.
		if !ok || mine != id {
			web.Error(w, http.StatusNotFound, "not found")
			return
		}
		h, err := store.Get(r.Context(), id)
		if errors.Is(err, households.ErrNotFound) {
			web.Error(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			web.Error(w, http.StatusInternalServerError, "get failed")
			return
		}
		web.JSON(w, http.StatusOK, h)
	}
}
```

- [ ] **Step 4: Wire `backend/internal/router/router.go`**

Extend `Deps` and `New` (guard nils so existing #7 tests that omit the new fields still pass):

```go
// Deps are the runtime dependencies the router wires into handlers.
type Deps struct {
	DB             Pinger
	Suggest        Suggester
	AuthMiddleware func(http.Handler) http.Handler
	Households     HouseholdStore
}
```

In `New`, after `r.Use(web.DeviceIDMiddleware)`:

```go
	if deps.AuthMiddleware != nil {
		r.Use(deps.AuthMiddleware)
	}
```

and replace the `/v1` group with:

```go
	r.Route("/v1", func(r chi.Router) {
		r.Get("/suggest", suggestHandler(deps.Suggest))
		if deps.Households != nil {
			r.Group(func(r chi.Router) {
				r.Use(auth.RequireAuth)
				r.Post("/households", createHousehold(deps.Households))
				r.Post("/households/join", joinHousehold(deps.Households))
				r.Get("/households/{id}", getHousehold(deps.Households))
			})
		}
	})
```

Add imports `"github.com/stroem/shopping-list/backend/internal/auth"` to `router.go`.

- [ ] **Step 5: Run — expect PASS.** `cd backend && go test ./internal/router/ && go vet ./internal/router/`

- [ ] **Step 6: Commit**

```bash
git add backend/internal/router/households.go backend/internal/router/households_handler_test.go backend/internal/router/router.go
git commit -m "feat(router): household create/join/get endpoints behind RequireAuth"
```

---

### Task 8: Suggest seam reads the authenticated household (TDD)

**Files:**
- Modify: `backend/internal/suggest/suggest.go`
- Create: `backend/internal/suggest/suggest_principal_test.go`

**Interfaces:**
- Consumes: `web.HouseholdID`.

- [ ] **Step 1: Write the failing test** — `backend/internal/suggest/suggest_principal_test.go`

```go
package suggest

import (
	"context"
	"testing"

	"github.com/stroem/shopping-list/backend/internal/web"
)

// ctxHouseholdQuerier records the household id bound into the suggest query ($2).
type ctxHouseholdQuerier struct{ gotHousehold any }

func (c *ctxHouseholdQuerier) Query(_ context.Context, _ string, args ...any) (rowsStub, error) {
	c.gotHousehold = args[1]
	return rowsStub{}, nil
}

func TestResolveHousehold_PrefersPrincipal(t *testing.T) {
	s := New(nil) // db unused: resolveHousehold returns before any query when principal present
	hh := "h-principal"
	ctx := web.WithPrincipal(context.Background(), web.Principal{UserID: "u", HouseholdID: &hh})

	got, err := s.resolveHousehold(ctx, "ignored-device")
	if err != nil || got == nil || *got != "h-principal" {
		t.Fatalf("resolveHousehold = %v err=%v, want h-principal", got, err)
	}
}
```

Note: `resolveHousehold` is unexported, so this test is in `package suggest` (white-box). It needs no DB because the principal short-circuits before any query. (Delete the unused `ctxHouseholdQuerier`/`rowsStub` scaffold if it doesn't compile — only the principal-precedence assertion matters; keep the test minimal.)

Minimal version (use this, drop the stub):

```go
package suggest

import (
	"context"
	"testing"

	"github.com/stroem/shopping-list/backend/internal/web"
)

func TestResolveHousehold_PrefersPrincipal(t *testing.T) {
	s := New(nil)
	hh := "h-principal"
	ctx := web.WithPrincipal(context.Background(), web.Principal{UserID: "u", HouseholdID: &hh})

	got, err := s.resolveHousehold(ctx, "ignored-device")
	if err != nil || got == nil || *got != "h-principal" {
		t.Fatalf("resolveHousehold = %v err=%v, want h-principal", got, err)
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (device path hits nil db / wrong value). `cd backend && go test ./internal/suggest/ -run TestResolveHousehold`

- [ ] **Step 3: Update `resolveHousehold` in `backend/internal/suggest/suggest.go`**

Add the principal check at the top and import `web`:

```go
func (s *Service) resolveHousehold(ctx context.Context, deviceID string) (*string, error) {
	// Prefer the authenticated household set by the auth middleware (#8). Falls
	// back to the provisional X-Device-Id path when there is no principal.
	if hid, ok := web.HouseholdID(ctx); ok {
		return &hid, nil
	}
	if deviceID == "" {
		return nil, nil
	}
	// ... existing X-Device-Id lookup unchanged ...
}
```

Add `"github.com/stroem/shopping-list/backend/internal/web"` to the imports.

- [ ] **Step 4: Run — expect PASS; full suggest suite green.** `cd backend && go test ./internal/suggest/ && go vet ./internal/suggest/`

- [ ] **Step 5: Commit**

```bash
git add backend/internal/suggest/suggest.go backend/internal/suggest/suggest_principal_test.go
git commit -m "feat(suggest): scope by authenticated household when present"
```

---

### Task 9: Entrypoint wiring + hermetic e2e + AGENTS.md

**Files:**
- Modify: `backend/cmd/api/main.go`, `backend/cmd/lambda/main.go`, `AGENTS.md`
- Create: `backend/internal/auth/e2e_test.go`

**Interfaces:**
- Consumes: everything above.

- [ ] **Step 1: Wire `backend/cmd/api/main.go`**

Add a verifier-construction helper and pass the new deps. After the pool is created:

```go
	verifier := buildVerifier(ctx, cfg)
	authMW := auth.Middleware(verifier, auth.NewUserStore(pool))

	srv := &http.Server{
		Addr: ":" + cfg.Port,
		Handler: router.New(router.Deps{
			DB:             pool,
			Suggest:        suggest.New(pool),
			AuthMiddleware: authMW,
			Households:     households.NewStore(pool),
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}
```

and add the helper + imports:

```go
// buildVerifier returns an OIDC verifier when an audience is configured, else a
// deny verifier so the server still boots locally.
func buildVerifier(ctx context.Context, cfg config.Config) auth.TokenVerifier {
	if cfg.OIDCAudience == "" {
		log.Print("OIDC_AUDIENCE unset — auth disabled (auth endpoints return 401)")
		return auth.NewDenyVerifier()
	}
	v, err := auth.NewOIDCVerifier(ctx, cfg.OIDCIssuer, cfg.OIDCAudience)
	if err != nil {
		log.Fatalf("oidc: %v", err)
	}
	return v
}
```

Imports to add: `"github.com/stroem/shopping-list/backend/internal/auth"`, `"github.com/stroem/shopping-list/backend/internal/households"`.

- [ ] **Step 2: Wire `backend/cmd/lambda/main.go`**

```go
	verifier := auth.NewDenyVerifier()
	if databaseURL != "" && os.Getenv("OIDC_AUDIENCE") != "" {
		v, err := auth.NewOIDCVerifier(initCtx, getenvDefault("OIDC_ISSUER", "https://accounts.google.com"), os.Getenv("OIDC_AUDIENCE"))
		if err != nil {
			log.Fatalf("oidc: %v", err)
		}
		verifier = v
	}
	adapter := httpadapter.NewV2(router.New(router.Deps{
		DB:             pool,
		Suggest:        suggest.New(pool),
		AuthMiddleware: auth.Middleware(verifier, auth.NewUserStore(pool)),
		Households:     households.NewStore(pool),
	}))
```

Add a small `getenvDefault(key, def string) string` helper and imports
`"github.com/stroem/shopping-list/backend/internal/auth"`,
`"github.com/stroem/shopping-list/backend/internal/households"`,
`"github.com/stroem/shopping-list/backend/internal/suggest"` (if not already present).

- [ ] **Step 3: Write the hermetic e2e** — `backend/internal/auth/e2e_test.go`

```go
package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/oauth2-proxy/mockoidc"

	"github.com/stroem/shopping-list/backend/internal/auth"
	"github.com/stroem/shopping-list/backend/internal/households"
	"github.com/stroem/shopping-list/backend/internal/router"
	"github.com/stroem/shopping-list/backend/internal/suggest"
)

type okPinger struct{}

func (okPinger) Ping(context.Context) error { return nil }

func mint(t *testing.T, m *mockoidc.MockOIDC, sub string) string {
	t.Helper()
	now := time.Now()
	tok, err := m.Keypair.SignJWT(jwt.MapClaims{
		"iss": m.Issuer(), "aud": m.Config().ClientID, "sub": sub,
		"email": sub + "@example.com", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	return tok
}

func TestHouseholdFlow_EndToEnd(t *testing.T) {
	m, err := mockoidc.Run()
	if err != nil {
		t.Fatalf("mockoidc: %v", err)
	}
	defer m.Shutdown()

	ctx := context.Background()
	pool := newPool(t) // from store_test.go (same package), skips without docker
	verifier, err := auth.NewOIDCVerifier(ctx, m.Issuer(), m.Config().ClientID)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}
	h := router.New(router.Deps{
		DB:             okPinger{},
		Suggest:        suggest.New(pool),
		AuthMiddleware: auth.Middleware(verifier, auth.NewUserStore(pool)),
		Households:     households.NewStore(pool),
	})

	call := func(method, path, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// Person A creates a household.
	recA := call(http.MethodPost, "/v1/households", mint(t, m, "personA"), `{"name":"Home"}`)
	if recA.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", recA.Code, recA.Body)
	}
	var created households.Household
	_ = json.Unmarshal(recA.Body.Bytes(), &created)
	if created.InviteCode == "" {
		t.Fatal("no invite code")
	}

	// Person B joins with the code.
	recB := call(http.MethodPost, "/v1/households/join", mint(t, m, "personB"),
		`{"invite_code":"`+created.InviteCode+`"}`)
	if recB.Code != http.StatusOK {
		t.Fatalf("join = %d: %s", recB.Code, recB.Body)
	}

	// Person C (no membership) cannot see the household → 404.
	recC := call(http.MethodGet, "/v1/households/"+created.ID, mint(t, m, "personC"), "")
	if recC.Code != http.StatusNotFound {
		t.Fatalf("stranger get = %d, want 404", recC.Code)
	}

	// Unauthenticated → 401.
	if rec := call(http.MethodPost, "/v1/households", "", `{}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no-auth create = %d, want 401", rec.Code)
	}
}
```

- [ ] **Step 4: Update `AGENTS.md`** — replace the identity lines in the "Sync & sharing" section.

Change the bullet that reads (around the `Sharing / auth` line):

```
- **Sharing / auth:** a household is a shared-secret UUID (join code) + per-device
  `X-Device-Id`. **No OAuth in v1.** Everything is household-scoped; cross-household
  access returns **404** (no existence leak).
```

to:

```
- **Sharing / auth:** sign-in is **Google / OIDC** — the backend verifies an ID
  token (`Authorization: Bearer`, configurable issuer via `OIDC_ISSUER` /
  `OIDC_AUDIENCE`) and keys a person by `(issuer, subject)`, auto-creating the
  `users` row on first request. People **share a household via an invite code**
  (`households.invite_code`, carried by a shareable link). Everything is
  household-scoped; cross-household access returns **404** (no existence leak).
  `X-Device-Id` is retained only for future device/sync use, not identity.
```

- [ ] **Step 5: Build, vet, full test**

Run: `cd backend && go build ./... && go vet ./... && go test ./...`
Expected: all green (auth/households/router/suggest unit + e2e pass with docker, skip cleanly without).

- [ ] **Step 6: Commit**

```bash
git add backend/cmd/api/main.go backend/cmd/lambda/main.go backend/internal/auth/e2e_test.go AGENTS.md
git commit -m "feat(auth): wire OIDC auth + households into entrypoints; e2e; docs"
```

---

### Task 10: Real end-to-end smoke (no new code)

**Files:** none.

- [ ] **Step 1: Boot the server with auth disabled, confirm baseline**

```bash
cid=$(podman run -d --rm -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=shopping_list -p 55435:5432 postgres:16-alpine)
sleep 4
export DATABASE_URL="postgres://postgres:postgres@localhost:55435/shopping_list?sslmode=disable"
cd backend && go run ./cmd/migrate up
PORT=8087 go run ./cmd/api &   # OIDC_AUDIENCE unset → auth disabled
sleep 2
curl -s -o /dev/null -w "healthz=%{http_code}\n" http://localhost:8087/healthz
curl -s -o /dev/null -w "suggest_open=%{http_code}\n" 'http://localhost:8087/v1/suggest?q=a'
curl -s -o /dev/null -w "create_noauth=%{http_code}\n" -X POST http://localhost:8087/v1/households
curl -s -o /dev/null -w "create_badtoken=%{http_code}\n" -X POST -H 'Authorization: Bearer garbage' http://localhost:8087/v1/households
```

Expected: `healthz=200`, `suggest_open=200` (baseline preserved), `create_noauth=401` (RequireAuth), `create_badtoken=401` (denyVerifier rejects).

- [ ] **Step 2: Stop server + container**

```bash
# find and kill the api by its listening port, then stop postgres
apipid=$(ss -ltnp 2>/dev/null | grep ':8087' | grep -o 'pid=[0-9]*' | head -1 | cut -d= -f2); [ -n "$apipid" ] && kill "$apipid"
podman stop "$cid"
```

No commit (verification only).

---

## Self-Review

**Spec coverage:**
- Migration 0003 (issuer/subject/email, nullable device_id, invite_code) → Task 1. ✓
- Config OIDC_ISSUER/OIDC_AUDIENCE optional → Task 1. ✓
- `web.Principal` plumbing → Task 2. ✓
- TokenVerifier + denyVerifier + UserStore auto-create (AC3) → Task 3. ✓
- Optional-auth middleware (401 invalid / pass absent) + RequireAuth → Task 4. ✓
- OIDCVerifier (go-oidc, configurable issuer) + hermetic mockoidc test → Task 5. ✓
- households store: create+invite_code (AC1), join-by-code (AC2), get → Task 6. ✓
- Endpoints + cross-household 404 (AC4) + router wiring with nil-guards → Task 7. ✓
- Suggest seam swap → Task 8. ✓
- Entrypoint wiring (both), hermetic household e2e, AGENTS.md pivot → Task 9. ✓
- Real boot smoke (baseline preserved, 401s) → Task 10. ✓

**Placeholder scan:** No TBD/TODO. The one runtime caveat (mockoidc minting API may differ by version) is an explicit "adjust to installed API" instruction with the invariant stated, not a content gap.

**Type consistency:** `Claims{Subject,Issuer,Email}` and `TokenVerifier.Verify(ctx, string) (Claims, error)` consistent across verifier, middleware, OIDC impl, fakes. `UserStore.Upsert(ctx, issuer, subject, email) (string, *string, error)` consistent in interface, `PgUserStore`, fake, middleware call. `web.Principal{UserID, HouseholdID *string, Email}` + `HouseholdID(ctx)(string,bool)` consistent across web, auth, suggest, router. `households.Household{ID, Name *string, InviteCode}` + `ErrNotFound` + `HouseholdStore` interface consistent across store, handlers, fakes, e2e. `Deps{DB, Suggest, AuthMiddleware, Households}` set in Task 7 and both entrypoints (Task 9); nil-guarded so the #7 tests that omit the new fields still compile and pass.
