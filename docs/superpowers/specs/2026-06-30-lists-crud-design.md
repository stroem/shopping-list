# Lists CRUD — Design (#9)

**Milestone:** M2 — Core domain API
**Status:** Approved
**Builds on:** #8 (OIDC identity + `Principal`/`HouseholdID` context, `RequireAuth`), `0001_init` schema.

## Summary

Household-scoped shopping lists with create, rename, archive, and soft-delete.
The `lists` table and its `lists_sync (household_id, updated_at)` index already
exist from `0001_init`, so **this is purely the API layer** — no migration.

A person authenticates via OIDC (from #8); every operation is scoped to that
person's household. List ids are **client-generated UUIDs**, so creation uses
`PUT /v1/lists/{id}` (the resource URL is known before it exists) and is
idempotent: the same PUT replayed never creates a duplicate.

## Acceptance criteria (from the issue)

- **AC1** Create, rename, archive, and soft-delete lists.
- **AC2** Client-generated UUID ids (idempotent create).
- **AC3** All operations are household-scoped; **404 on foreign ids** (no existence leak).
- **AC4** `updated_at` / `deleted_at` maintained for sync.

## Existing schema (no change)

```sql
CREATE TABLE lists (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    household_id uuid NOT NULL REFERENCES households(id),
    name         text NOT NULL,
    archived_at  timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    deleted_at   timestamptz
);
CREATE INDEX lists_sync ON lists (household_id, updated_at);
```

`archived_at` (reversible state) and `deleted_at` (terminal soft-delete) are
distinct. Soft delete is never a hard delete — the row persists so #12's
pull-sync can propagate the tombstone.

## API surface

All routes are under `/v1` and behind the existing `RequireAuth` group; the
household is derived from the authenticated `Principal`, never from the client.

| Method   | Path           | Purpose                                  | Success                       |
|----------|----------------|------------------------------------------|-------------------------------|
| `PUT`    | `/lists/{id}`  | Create-or-replace (client UUID), body `{name}` | `201` create / `200` idempotent repeat |
| `GET`    | `/lists`       | List the household's lists               | `200` `[]List`                |
| `GET`    | `/lists/{id}`  | Fetch one                                | `200` / `404`                 |
| `PATCH`  | `/lists/{id}`  | Rename and/or (un)archive, body `{name?, archived?}` | `200`             |
| `DELETE` | `/lists/{id}`  | Soft-delete (`deleted_at`)               | `204`                         |

### Method rationale (REST)

- **PUT for create**: the client generates the id, so the resource URI is known
  in advance — PUT is the idempotent "create-or-replace at this known URI"
  method (RFC 9110). POST (server-assigned URI, non-idempotent) would fight the
  semantics.
- **PATCH for rename/archive**: a partial state edit on a live resource.
  `archived: true` sets `archived_at = now()`; `archived: false` clears it
  (reversible).
- **DELETE for soft-delete**: from the client's perspective the list is gone
  (`204`, and subsequent `GET` → `404`). Retaining the row for sync is an
  implementation detail that does not change the verb.

PUT and PATCH overlap on `name` (PUT replaces it, PATCH can also set it). This
overlap is intentional and accepted: PUT is the create/replace path, PATCH is
the partial-edit path used for archive (and rename when the client prefers a
partial update).

### Request/response shapes

```jsonc
// List (response)
{
  "id":          "uuid",
  "name":        "Groceries",
  "archived_at": null,            // RFC3339 timestamp when archived, else null
  "created_at":  "2026-06-30T...",
  "updated_at":  "2026-06-30T...",
  "deleted_at":  null             // present in the model; soft-deleted rows are
                                   // excluded from GET responses (sync reads it)
}

// PUT /v1/lists/{id}     body: {"name": "Groceries"}        name required, non-empty
// PATCH /v1/lists/{id}   body: {"name"?: "x", "archived"?: true|false}  at least one field
```

## Components

### `internal/lists` (new package)

Mirrors `internal/households`.

```go
package lists

var ErrNotFound = errors.New("list not found")

type List struct {
    ID         string     `json:"id"`
    Name       string     `json:"name"`
    ArchivedAt *time.Time `json:"archived_at"`
    CreatedAt  time.Time  `json:"created_at"`
    UpdatedAt  time.Time  `json:"updated_at"`
    DeletedAt  *time.Time `json:"deleted_at"`
}

type Store struct{ db *pgxpool.Pool }
func NewStore(db *pgxpool.Pool) *Store

// Upsert creates-or-replaces the list for (householdID, id). created reports
// whether a new row was inserted (true → 201) vs updated (false → 200), via the
// xmax=0 RETURNING trick. Guarded by household_id so a foreign id cannot be
// hijacked: ON CONFLICT (id) DO UPDATE ... WHERE lists.household_id = $hh. When
// the id already exists in another household — OR in this household but
// soft-deleted — the guarded UPDATE matches no row and RETURNING is empty →
// Upsert returns ErrNotFound (handler maps to 404, no existence leak, delete
// stays terminal). Conflict guard: ON CONFLICT (id) DO UPDATE ... WHERE
// lists.household_id = $hh AND lists.deleted_at IS NULL.
func (s *Store) Upsert(ctx, householdID, id, name string) (l List, created bool, err error)

// List returns the household's non-deleted lists (archived included), newest first.
func (s *Store) List(ctx, householdID string) ([]List, error)

// Get returns one non-deleted list scoped to the household, or ErrNotFound.
func (s *Store) Get(ctx, householdID, id string) (List, error)

// Rename and/or (un)archive. name==nil leaves the name; archived==nil leaves the
// archive state. Scoped to household; ErrNotFound if absent/foreign/deleted.
func (s *Store) Update(ctx, householdID, id string, name *string, archived *bool) (List, error)

// SoftDelete sets deleted_at; idempotent; ErrNotFound if absent/foreign.
func (s *Store) SoftDelete(ctx, householdID, id string) error
```

All statements carry `AND household_id = $hh AND deleted_at IS NULL` (except the
Upsert conflict guard, which keys on the unique `id`). Every mutation sets
`updated_at = now()`.

### `internal/router/lists.go` (new)

Thin handlers following `router/households.go`:

- A `ListStore` interface (the methods above) so handlers take a fake in tests.
- `putList`, `listLists`, `getList`, `patchList`, `deleteList`.
- Decode the path id with `chi.URLParam`; validate it parses as a UUID →
  `400` if not. Validate body (`name` non-empty for PUT; PATCH needs ≥1 field).
- Map `lists.ErrNotFound` → `404`; other errors → `500`. Use `web.JSON`/`web.Error`.
- Household comes from `web.HouseholdID(ctx)`; if the caller has no household
  yet, every list route returns `404` (nothing is theirs).

### `internal/router/router.go` (modify)

Add `Lists ListStore` to `Deps`. Inside the existing `RequireAuth` group
(nil-guarded like `Households`), register the five routes.

### Entrypoints (`cmd/api`, `cmd/lambda`) (modify)

Wire `Lists: lists.NewStore(pool)` into `router.Deps`, alongside `Households`.

## Scoping & error model

- **Foreign / missing / deleted id → 404** everywhere (`GET`, `PATCH`, `DELETE`,
  and `PUT` when the id exists in another household). No 403, no existence leak.
- **No household yet → 404** on all `{id}` routes; `GET /lists` returns `[]`.
- **Invalid UUID in path → 400.**
- **Empty `name` on PUT, or empty PATCH body → 400.**

## Testing

Store tests use testcontainers Postgres (skip cleanly when Docker/`DATABASE_URL`
is absent), reusing the `newPool` helper pattern from `households/store_test.go`:

- PUT create returns `created=true`; replaying the same PUT returns
  `created=false` with no duplicate row (AC2).
- Rename via `Update`; archive then unarchive toggles `archived_at` (AC1).
- `SoftDelete` sets `deleted_at`; subsequent `Get` → `ErrNotFound` (AC1, AC4).
- After `SoftDelete`, re-`Upsert` of the same id by its own household →
  `ErrNotFound` (delete is terminal; no accidental resurrection).
- `List` excludes soft-deleted, includes archived.
- Cross-household: a list created by household A is `ErrNotFound` for B on Get/
  Update/SoftDelete; A's Upsert cannot overwrite B's id (AC3).
- `updated_at` advances on each mutation (AC4).

Handler tests use a fake `ListStore`: status codes (`201` vs `200` on PUT,
`204` on DELETE, `404` on foreign/missing, `400` on bad UUID / empty body), and
that the household id is taken from the principal, not the body.

Green bar: `go test ./...`.

## Out of scope (YAGNI)

- List **items** (#10), check-off (#11), pull-sync endpoints (#12).
- Pagination on `GET /lists` (a household has few lists).
- Hard delete / purge of tombstones.
- Restoring a soft-deleted list (no AC; revisit if needed).
