# Households & identity (OIDC) — issue #8 design

**Status:** approved 2026-06-28
**Issue:** [#8 — Households and device identity](https://github.com/stroem/shopping-list/issues/8)
(milestone *M2 — Core domain API*)
**Builds on:** #2 (schema: `households`, `users`), #3 (`config`, router, `web`),
#7 (`/v1` group, the suggest household seam).

## Goal

Establish the **identity foundation** every household-scoped M2 endpoint sits on.
Pivoting from the issue's original "anonymous device + join-code" wording to the
product owner's chosen direction: **Google / OIDC sign-in identifies a person**,
and people **share a household via an invite code** (the shareable link carries
the code). Backend verifies an OIDC **ID token**; a person is keyed by
`(issuer, subject)`.

## Why OIDC now is testable without the app

The Flutter Google Sign-In UI (#13/#19) does not exist yet, but OIDC is **fully
e2e-testable without it**: most tests inject a **fake `TokenVerifier`**; the real
verification path (signature, `iss`/`aud`/`exp`) is proven hermetically by an
**in-process mock OIDC provider** (`github.com/oauth2-proxy/mockoidc`) with a
**configurable issuer/audience**. No UI, no real Google, deterministic.

## Acceptance criteria (reinterpreted for OIDC)

- **AC1** Create household → returns the invite code.
- **AC2** Join household by code → associates the caller with it.
- **AC3** First authenticated request from an unknown `(issuer, subject)`
  **auto-creates a `users` row** (replaces "unknown `X-Device-Id`").
- **AC4** Cross-household access returns **404** (no existence leak).

## Schema — migration `0003_oidc_identity`

```sql
-- users become a person keyed by OIDC identity; device_id is no longer identity
ALTER TABLE users
    ADD COLUMN issuer  text,
    ADD COLUMN subject text,
    ADD COLUMN email   text,
    ALTER COLUMN device_id DROP NOT NULL;
CREATE UNIQUE INDEX users_issuer_subject ON users (issuer, subject);

-- households gain an unguessable, shareable invite code
ALTER TABLE households
    ADD COLUMN invite_code text NOT NULL DEFAULT gen_random_uuid()::text;
CREATE UNIQUE INDEX households_invite_code ON households (invite_code);
```

`device_id` stays as a nullable column (no longer the identity; a devices table
for sync is a future concern — YAGNI). The `UNIQUE(issuer, subject)` index keys a
person; `gen_random_uuid()::text` gives each household an unguessable invite code.

## Components

### `internal/auth`
- `type Claims struct { Subject, Issuer, Email string }`
- `type TokenVerifier interface { Verify(ctx, rawToken string) (Claims, error) }`
- `OIDCVerifier` — wraps `go-oidc` (`oidc.NewProvider` + `Verifier`); built from a
  configurable issuer + audience. Extracts `Subject`/`Issuer` from the verified
  `IDToken` and `email` from its claims. `denyVerifier` (always errors) is used
  when no audience is configured, so the server still boots.
- `type UserStore interface { Upsert(ctx, issuer, subject, email string) (userID string, householdID *string, err error) }` — pgx-backed
  `INSERT INTO users (issuer, subject, email) VALUES (...) ON CONFLICT (issuer,
  subject) DO UPDATE SET email = EXCLUDED.email, updated_at = now() RETURNING
  id::text, household_id::text`. Auto-creates on first sight (**AC3**).
- `Middleware(verifier, store)` — **optional auth**: no `Authorization: Bearer`
  header → continue with no principal; present → verify (invalid → **401 JSON**),
  upsert the user, put `web.Principal` in context.
- `RequireAuth(next)` — 401 when no principal is in context (guards the household
  endpoints; an authenticated user with no household yet still passes).

### `internal/web` (additions)
- `type Principal struct { UserID string; HouseholdID *string; Email string }`
- `WithPrincipal(ctx, Principal) ctx`, `PrincipalFrom(ctx) (Principal, bool)`,
  and `HouseholdID(ctx) (string, bool)` (convenience; false when no principal or
  no household). The auth middleware sets these; suggest and the household
  handlers read them.

### `internal/households`
- `type Household struct { ID string; Name *string; InviteCode string }`
- `Store{db}` with:
  - `Create(ctx, userID string, name *string) (Household, error)` — insert the
    household, set the caller's `household_id` (one transaction), return it incl.
    `invite_code`.
  - `JoinByCode(ctx, userID, code string) (Household, error)` — find by
    `invite_code` (`ErrNotFound` if none), set the caller's `household_id`.
  - `Get(ctx, id string) (Household, error)` — fetch by id (`ErrNotFound` if none).

### `internal/router` (handlers + wiring)
- `POST /v1/households` (RequireAuth) → `Create` → `201 {id, name, invite_code}` (**AC1**).
- `POST /v1/households/join` (RequireAuth) body `{invite_code}` → `JoinByCode` →
  `200 {household}`; unknown code → **404** (**AC2**).
- `GET /v1/households/{id}` (RequireAuth) → **404 unless `id` equals the caller's
  `HouseholdID`** (even if the household exists) (**AC4**, no existence leak).
- `Deps` gains the optional-auth middleware + the households store; `New` applies
  the middleware globally (after `DeviceIDMiddleware`) and mounts the household
  routes under `/v1` wrapped in `RequireAuth`. `/v1/suggest` stays open.

### `internal/suggest` (seam swap — additive)
- `resolveHousehold` reads `web.HouseholdID(ctx)` **first** (set by the auth
  middleware for an authenticated caller); falls back to the existing
  `X-Device-Id → users` path when absent. #7's behavior and tests are unchanged
  (no principal in those tests ⇒ device path); authenticated requests now use the
  real household. The ranking query is untouched.

### Config (`internal/config`)
- `OIDCIssuer` (env `OIDC_ISSUER`, default `https://accounts.google.com`) and
  `OIDCAudience` (env `OIDC_AUDIENCE`, optional). When audience is empty the
  server boots with the `denyVerifier` (auth endpoints 401, `/healthz` + suggest
  still work) — the local-dev baseline is preserved. `.env.example` documents both.

### Entrypoints (`cmd/api`, `cmd/lambda`)
- Construct the verifier (`OIDCVerifier` when `OIDCAudience` set, else
  `denyVerifier`), the `UserStore`, the auth middleware, and the households store
  from the pool; pass them in `Deps`. Same wiring both places.

## Data flow

```
Authorization: Bearer <google id token>
  → auth.Middleware: verify (go-oidc, configurable issuer)
  → Claims{subject, issuer, email}
  → UserStore.Upsert → (userID, householdID)         // auto-create (AC3)
  → web.Principal in context
  → RequireAuth-guarded handler reads Principal
       POST /v1/households       → create + invite_code (AC1)
       POST /v1/households/join  → associate by code   (AC2)
       GET  /v1/households/{id}  → 404 if not caller's  (AC4)
  → /v1/suggest reads web.HouseholdID(ctx) for scoping
```

## Error handling

- Missing `Authorization` → no principal (open endpoints proceed; RequireAuth
  endpoints 401).
- Invalid/expired token → **401** JSON.
- Unknown invite code on join, or `GET` of a household that isn't the caller's →
  **404** (identical response for "doesn't exist" and "not yours" — no leak).
- Malformed JSON body → 400.
- DB errors → JSON 500 (`web.Error` / `web.Recoverer`).

## Testing (green bar `go test ./...`, docker-optional)

- **`auth` middleware (no DB, no network):** fake `TokenVerifier` + fake
  `UserStore`. No header → handler sees no principal; invalid token → 401; valid →
  principal set, user upserted with right claims; `RequireAuth` → 401 without a
  principal, passes with one.
- **`OIDCVerifier` (mockoidc, in-process, no docker):** start `mockoidc`, build
  the verifier against its issuer/audience, mint an ID token, `Verify` → claims
  match; a wrong-audience / expired token → error.
- **`UserStore` + `households.Store` (testcontainers):** Upsert auto-creates then
  updates the same row; `Create` sets the caller's household and returns an invite
  code; `JoinByCode` associates and `ErrNotFound` on a bad code; `Get`.
- **Router handlers (fake stores/principal):** create → `201` with invite_code;
  join unknown code → 404; `GET` not-mine → 404, mine → 200; RequireAuth → 401.
- **Hermetic e2e (mockoidc + testcontainers):** mint a token for person A → `POST
  /v1/households` → invite_code; mint a token for person B → `POST
  /v1/households/join {code}` → both share the household; a third person `GET`s
  that household id → **404**. Proves the whole flow with no UI and no real Google.

## Documentation

- **AGENTS.md updated** to record the identity pivot: OIDC/Google ID-token
  sign-in (configurable issuer), person keyed by `(issuer, subject)`, household
  sharing by invite code — replacing the "no OAuth v1 / shared-secret UUID +
  X-Device-Id" lines. This issue owns that change.

## Out of scope (owned elsewhere)

The Flutter Google Sign-In UI and invite-link deep-linking (#13/#19) · a devices
table / per-device sync identity (future, with #12) · leaving/switching
households, multiple households per person, roles · token refresh (the client
owns its Google session) · rate limiting. #8 ships the backend identity +
household membership only.
