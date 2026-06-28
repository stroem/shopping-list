# Full-featured `cmd/migrate` CLI — design

**Issue:** [#25](https://github.com/stroem/shopping-list/issues/25) — Full-featured migrate command (cmd/migrate CLI)
**Milestone:** M0 — Foundation & infra
**Date:** 2026-06-28
**Status:** approved

## Problem

[#3](https://github.com/stroem/shopping-list/issues/3) added a **minimal** `cmd/migrate`
(`up` / `down` only) so local dev and `just migrate` work. We need to grow it into a
proper migration CLI usable from the deploy/CI path (#4) as a release step: report the
current schema version, recover from a dirty state, and apply/revert a bounded number of
steps or migrate to a specific version.

## Decision: keep golang-migrate

The backend already standardizes on `github.com/golang-migrate/migrate/v4`: two SQL
migrations exist (`0001_init`, `0002_ean_mappings_details`), embedded via
`backend/migrations/embed.go`; `internal/db.Migrate`/`MigrateDown` wrap it; `cmd/api`
runs it on startup; tests and `just migrate` are wired to it. Data access elsewhere is
**raw `pgx`/`pgxpool`, no ORM** (per `AGENTS.md`).

golang-migrate's library already exposes exactly the operations #25 asks for
(`Version`, `Force`, `Steps`, `Migrate`), so this ticket **surfaces existing capability**
rather than building a migration engine. Switching to an alternative engine (e.g.
`uptrace/bun`'s `bun/migrate`) was considered and rejected for #25: it would add an ORM
dependency the project deliberately avoids, require rewriting the existing migrations +
`cmd/api` startup + tests, and exceeds this ticket's scope. If bun/ORM adoption is ever
wanted, it is a separate backend-wide evaluation.

## Scope

In scope (acceptance criteria from #25):

- Subcommands beyond `up`/`down`: `version`, `force <v>`, `steps <n>`, `goto <v>`.
- Reads `DATABASE_URL`; clear usage/help; non-zero exits on misuse.
- Reuses the embedded migrations + `internal/db` runner — **no external migrate CLI
  dependency** (golang-migrate stays a library used only inside `internal/db`).
- Consumable by the deploy path (#4) as a release step (`migrate up`, non-interactive).

Out of scope: changing migration file format, adding an ORM, creating new migrations,
altering `cmd/api`'s startup-migrate behavior.

## CLI surface (`backend/cmd/migrate`)

| Command      | Behavior                                                          | Destructive? |
|--------------|------------------------------------------------------------------|--------------|
| `up`         | apply all pending migrations (idempotent) — *existing*           | no           |
| `down`       | revert **all** migrations — *existing, backward compatible*      | yes → confirm|
| `version`    | print current version + dirty state; `no migrations applied` if none | no       |
| `force <v>`  | set version to `v`, clear the dirty flag (no migration run)      | yes → confirm|
| `steps <n>`  | apply `n` (n>0) or revert `abs(n)` (n<0) migrations              | yes if n<0 → confirm |
| `goto <v>`   | migrate up or down to target version `v` (`goto 0` = full revert)| yes if it reverts → confirm |

Global flags:

- `-y` / `--yes` — skip the confirmation prompt (for CI / scripted use).
- `-h` / `--help` — print usage to stdout, exit 0.

Parsing is **hand-rolled with the standard library** (no `cobra`/`urfave-cli`), consistent
with the existing dependency-light code. `<v>` and `<n>` are parsed with `strconv`; a
missing or non-numeric argument is a usage error.

### Assumption (baked in)

`down` keeps its existing meaning — **revert all migrations** — for backward compatibility
with `AGENTS.md`'s documented `go run ./cmd/migrate up|down` and the `just migrate` UX. The
finer-grained control comes from `steps`/`goto`. `down` is now treated as a destructive op
and goes through the confirmation gate.

## Confirmation (TTY-aware)

Destructive operations (`down`, `force`, and any `steps`/`goto` that reverts) proceed when:

```
--yes is set  OR  stdin is not a TTY  OR  the interactive y/N prompt is answered yes
```

Rationale: deploy/CI runs are non-interactive (no TTY) and must not hang on a prompt, so
they proceed unattended; a human at a terminal gets a `y/N` guard; `--yes` is the explicit
scripted override. TTY detection uses the standard library only —
`os.Stdin.Stat()` and `os.ModeCharDevice` — to avoid pulling in `golang.org/x/term`.

A `steps`/`goto` is classified as "reverts" only when its target is below the current
version. `version` and `up` are never destructive.

## Output & exit codes

- Human-readable results → **stdout** (e.g. `version: 2 (clean)`, `migrations applied`).
- Errors and usage text → **stderr**.
- Exit codes:
  - `0` — success.
  - `2` — usage error (unknown subcommand, missing/invalid `<v>`/`<n>`, extra args). Matches
    the current code's `os.Exit(2)` convention.
  - `1` — runtime failure (config load, DB connection, migration error), or a destructive op
    declined at the prompt.
- `version` always exits `0` and reports clean/dirty in text — it is informational, not a
  CI gate. (`no migrations applied` when the DB has no version row.)

## `internal/db` runner additions

The golang-migrate setup currently duplicated across `Migrate`/`MigrateDown` is factored
into one helper; new thin wrappers reuse the embedded `migrations.FS` + existing
`migrateURL` normalization. The golang-migrate dependency stays **inside `internal/db`**;
`cmd/migrate` imports only `internal/db` + `internal/config`.

```go
// newMigrator builds a *migrate.Migrate from the embedded migrations and a
// normalized URL. Shared by all runner functions; caller closes via m.Close().
func newMigrator(databaseURL string) (*migrate.Migrate, error)

// ErrNoMigrations is returned by Version when the database has no applied
// version (golang-migrate's ErrNilVersion), so cmd/migrate never imports the
// migrate package to detect it.
var ErrNoMigrations = errors.New("no migrations applied")

func Version(databaseURL string) (version uint, dirty bool, err error)
func Force(databaseURL string, version int) error
func Steps(databaseURL string, n int) error   // ErrNoChange treated as success
func Goto(databaseURL string, version uint) error  // m.Migrate(v); ErrNoChange = success
```

`Migrate` and `MigrateDown` are refactored to call `newMigrator` with **no behavior
change**. `ErrNoChange` continues to be treated as success where it already is.

Note on `ctx`: golang-migrate v4 does not thread a context into its operations (as the
existing `Migrate` doc comment records). New runner functions follow `MigrateDown`'s
signature and omit `ctx` rather than carrying an unused parameter.

## Testing (TDD)

Red → green per unit. Green bar = `go test ./...` (DB-backed tests skip cleanly without
`DATABASE_URL`).

**`internal/db` (DB-backed, skips without `DATABASE_URL`)** — mirror the existing
`migrate_test.go` container setup:

- `Version`: returns `ErrNoMigrations` on a fresh DB; returns the top version + `dirty=false`
  after `Migrate`; reports `dirty=true` after a forced bad/dirty state.
- `Steps`: `Steps(+1)` advances one version from clean; `Steps(-1)` reverts one; over-revert
  past zero surfaces an error, not a panic.
- `Goto`: `Goto(n)` reaches version `n` from both below and above; `Goto(0)` fully reverts.
- `Force`: clears a dirty state and sets the version, after which `Version` reports it clean.

**`cmd/migrate` (no DB needed)** — extract dispatch into a testable function:

```go
type migrator interface {
    Up() error
    DownAll() error
    Version() (version uint, dirty bool, applied bool, err error)
    Force(v int) error
    Steps(n int) error
    Goto(v uint) error
}

// run parses args, gates destructive ops, invokes m, and returns an exit code.
func run(args []string, stdout, stderr io.Writer, stdin io.Reader, isTTY bool, m migrator) int
```

A fake `migrator` (recording calls, optionally returning errors) drives tests for:

- command routing (each subcommand calls the right method with the parsed arg);
- usage errors → exit `2` (unknown subcommand, missing/non-numeric `<v>`/`<n>`, extra args);
- `--yes` skips confirmation; non-TTY proceeds unattended; TTY `n`/empty answer aborts a
  destructive op and returns a non-zero code without invoking the method;
- `version` output formatting for applied / not-applied / dirty;
- runtime error from a method → exit `1`.

`main()` is thin: load config, build the real db-backed `migrator` (closing over
`DATABASE_URL`), detect TTY from `os.Stdin`, and `os.Exit(run(...))`.

## Deploy-path consumption (#4)

The deploy/release step runs `migrate up` (idempotent, non-interactive, exits non-zero on
failure). No new contract beyond a stable command surface and exit codes; #4 wires it in
separately.

## Files touched

- `backend/cmd/migrate/main.go` — full CLI (dispatch, flags, confirmation, exit codes).
- `backend/cmd/migrate/main_test.go` — new; dispatch/usage/confirmation tests via fake.
- `backend/internal/db/migrate.go` — `newMigrator` helper + `Version`/`Force`/`Steps`/`Goto`
  + `ErrNoMigrations`; refactor `Migrate`/`MigrateDown`.
- `backend/internal/db/migrate_test.go` — new round-trip tests for the added runners.
- `AGENTS.md` — update the `cmd/migrate` line to list the full subcommand surface (proposed).
