# Full-featured migrate CLI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Grow `cmd/migrate` from `up`/`down`-only into a full CLI (`version`, `force`, `steps`, `goto`) on the existing golang-migrate runner.

**Architecture:** Add thin runner wrappers to `internal/db` over golang-migrate's `Version`/`Force`/`Steps`/`Migrate`, keeping the golang-migrate dependency inside `internal/db`. `cmd/migrate` gets a hand-rolled, testable `run(...)` dispatcher behind a small `migrator` interface so its parsing/confirmation/exit-code logic is unit-testable without a database. TTY-aware confirmation gates destructive ops.

**Tech Stack:** Go 1.26, golang-migrate v4 (library), pgx v5, testcontainers-go (DB tests).

## Global Constraints

- Reuse the embedded `migrations.FS` + `internal/db` runner. **No external migrate CLI dependency**; `cmd/migrate` imports only `internal/db` + `internal/config`, never `golang-migrate` directly.
- **No new third-party dependency.** Standard library only for the CLI (TTY via `os.ModeCharDevice`, no `golang.org/x/term`).
- Green bar = `go test ./...` from `backend/`. DB-backed tests **skip cleanly** when Docker/Postgres is unavailable (use the existing testcontainers skip pattern).
- Conventional Commits 1.0.0; **no AI/Claude attribution** in commit messages.
- Exit codes: `0` success · `2` usage error · `1` runtime failure or declined prompt.
- All `git` commands run inside the worktree `backend/`'s repo; commit only on `feat/issue-25-migrate-cli`.

---

### Task 1: `internal/db` runner additions

**Files:**
- Modify: `backend/internal/db/migrate.go` (add `newMigrator`, `ErrNoMigrations`, `Version`, `Force`, `Steps`, `Goto`; refactor `Migrate`/`MigrateDown` onto `newMigrator`)
- Test: `backend/internal/db/migrate_runner_test.go` (create)

**Interfaces:**
- Consumes: existing `migrateURL`, `migrations.FS`, `migrate`/`iofs` imports already in `migrate.go`.
- Produces (later tasks rely on these exact signatures):
  - `var ErrNoMigrations error`
  - `func Version(databaseURL string) (version uint, dirty bool, err error)` — returns `ErrNoMigrations` when no version row exists.
  - `func Force(databaseURL string, version int) error`
  - `func Steps(databaseURL string, n int) error`
  - `func Goto(databaseURL string, version uint) error` — `version == 0` reverts all.

- [ ] **Step 1: Write the failing tests**

Create `backend/internal/db/migrate_runner_test.go`:

```go
package db_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/stroem/shopping-list/backend/internal/db"
)

// startPostgres boots a throwaway Postgres and returns its URL, skipping the
// test when Docker is unavailable (mirrors TestMigrateUpDownRoundTrip).
func startPostgres(t *testing.T) string {
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
	url, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	return url
}

func TestVersionNoMigrations(t *testing.T) {
	url := startPostgres(t)
	if _, _, err := db.Version(url); !errors.Is(err, db.ErrNoMigrations) {
		t.Fatalf("Version on fresh db: got err %v, want ErrNoMigrations", err)
	}
}

func TestVersionAfterUp(t *testing.T) {
	url := startPostgres(t)
	if err := db.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	v, dirty, err := db.Version(url)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if v != 2 || dirty {
		t.Fatalf("Version after up = (%d, dirty=%v), want (2, false)", v, dirty)
	}
}

func TestStepsForwardAndBack(t *testing.T) {
	url := startPostgres(t)
	if err := db.Steps(url, 1); err != nil {
		t.Fatalf("Steps(+1): %v", err)
	}
	if v, _, _ := db.Version(url); v != 1 {
		t.Fatalf("after Steps(+1) version = %d, want 1", v)
	}
	if err := db.Steps(url, 1); err != nil {
		t.Fatalf("Steps(+1) again: %v", err)
	}
	if v, _, _ := db.Version(url); v != 2 {
		t.Fatalf("after second Steps(+1) version = %d, want 2", v)
	}
	if err := db.Steps(url, -1); err != nil {
		t.Fatalf("Steps(-1): %v", err)
	}
	if v, _, _ := db.Version(url); v != 1 {
		t.Fatalf("after Steps(-1) version = %d, want 1", v)
	}
}

func TestGotoUpThenZeroReverts(t *testing.T) {
	url := startPostgres(t)
	if err := db.Goto(url, 2); err != nil {
		t.Fatalf("Goto(2): %v", err)
	}
	if v, _, _ := db.Version(url); v != 2 {
		t.Fatalf("after Goto(2) version = %d, want 2", v)
	}
	if err := db.Goto(url, 0); err != nil {
		t.Fatalf("Goto(0): %v", err)
	}
	if _, _, err := db.Version(url); !errors.Is(err, db.ErrNoMigrations) {
		t.Fatalf("after Goto(0): got err %v, want ErrNoMigrations", err)
	}
}

func TestForceClearsDirty(t *testing.T) {
	url := startPostgres(t)
	if err := db.Migrate(context.Background(), url); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	// Simulate an interrupted migration: mark the version row dirty.
	setDirty(t, url)
	if _, dirty, err := db.Version(url); err != nil || !dirty {
		t.Fatalf("expected dirty version, got dirty=%v err=%v", dirty, err)
	}
	if err := db.Force(url, 2); err != nil {
		t.Fatalf("Force(2): %v", err)
	}
	v, dirty, err := db.Version(url)
	if err != nil || v != 2 || dirty {
		t.Fatalf("after Force(2) = (%d, dirty=%v, err=%v), want (2, false, nil)", v, dirty, err)
	}
}
```

- [ ] **Step 2: Add the `setDirty` test helper**

golang-migrate's pgx5 driver stores state in `schema_migrations(version bigint, dirty boolean)`. Append to `migrate_runner_test.go`:

```go
func setDirty(t *testing.T, url string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `UPDATE schema_migrations SET dirty = true`); err != nil {
		t.Fatalf("set dirty: %v", err)
	}
}
```

Add `"github.com/jackc/pgx/v5"` to this file's imports.

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/db/ -run 'TestVersion|TestSteps|TestGoto|TestForce' -v`
Expected: compile failure — `db.Version`, `db.Force`, `db.Steps`, `db.Goto`, `db.ErrNoMigrations` undefined. (If Docker is unavailable the DB tests would skip, but the compile error comes first and is the expected red here.)

- [ ] **Step 4: Implement `newMigrator` + the runner functions**

In `backend/internal/db/migrate.go`, add `"errors"` is already imported. Add after the `migrateURL` function:

```go
// ErrNoMigrations is returned by Version when the database has no applied
// migration version. It wraps golang-migrate's ErrNilVersion so callers
// (cmd/migrate) need not import the migrate package to detect the condition.
var ErrNoMigrations = errors.New("no migrations applied")

// newMigrator builds a *migrate.Migrate from the embedded migrations and a
// normalized URL. The caller must Close it.
func newMigrator(databaseURL string) (*migrate.Migrate, error) {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("load embedded migrations: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, migrateURL(databaseURL))
	if err != nil {
		return nil, fmt.Errorf("init migrator: %w", err)
	}
	return m, nil
}

// Version reports the current schema version and whether it is dirty (a
// migration failed partway). It returns ErrNoMigrations when no version is set.
func Version(databaseURL string) (uint, bool, error) {
	m, err := newMigrator(databaseURL)
	if err != nil {
		return 0, false, err
	}
	defer m.Close()
	v, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, ErrNoMigrations
	}
	if err != nil {
		return 0, false, fmt.Errorf("read version: %w", err)
	}
	return v, dirty, nil
}

// Force sets the schema version to version and clears the dirty flag without
// running any migration. Use it to recover from a dirty state.
func Force(databaseURL string, version int) error {
	m, err := newMigrator(databaseURL)
	if err != nil {
		return err
	}
	defer m.Close()
	if err := m.Force(version); err != nil {
		return fmt.Errorf("force version: %w", err)
	}
	return nil
}

// Steps applies n migrations (n>0) or reverts -n migrations (n<0).
func Steps(databaseURL string, n int) error {
	m, err := newMigrator(databaseURL)
	if err != nil {
		return err
	}
	defer m.Close()
	if err := m.Steps(n); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply steps: %w", err)
	}
	return nil
}

// Goto migrates up or down to the target version. version == 0 reverts all.
func Goto(databaseURL string, version uint) error {
	m, err := newMigrator(databaseURL)
	if err != nil {
		return err
	}
	defer m.Close()
	var migErr error
	if version == 0 {
		migErr = m.Down()
	} else {
		migErr = m.Migrate(version)
	}
	if migErr != nil && !errors.Is(migErr, migrate.ErrNoChange) {
		return fmt.Errorf("goto version: %w", migErr)
	}
	return nil
}
```

- [ ] **Step 5: Refactor `Migrate`/`MigrateDown` onto `newMigrator`**

Replace the body of `Migrate` (keep its doc comment + `_ = ctx`) so it uses the helper:

```go
func Migrate(ctx context.Context, databaseURL string) error {
	_ = ctx
	m, err := newMigrator(databaseURL)
	if err != nil {
		return err
	}
	defer m.Close()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
```

And `MigrateDown`:

```go
func MigrateDown(databaseURL string) error {
	m, err := newMigrator(databaseURL)
	if err != nil {
		return err
	}
	defer m.Close()
	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("revert migrations: %w", err)
	}
	return nil
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/db/ -v`
Expected: PASS (new runner tests + existing `TestMigrateUpDownRoundTrip` + `TestMigrateURL`). If Docker is unavailable, DB-backed tests SKIP and `TestMigrateURL` PASSES — that is an acceptable green.

- [ ] **Step 7: Vet + commit**

```bash
go vet ./internal/db/
git add internal/db/migrate.go internal/db/migrate_runner_test.go
git commit -m "feat(db): add Version/Force/Steps/Goto migration runners

Expose golang-migrate's version, force, steps and goto operations as thin
internal/db wrappers over a shared newMigrator helper, with an
ErrNoMigrations sentinel so callers need not import golang-migrate.

Part of #25"
```

---

### Task 2: `cmd/migrate` full CLI + docs

**Files:**
- Modify: `backend/cmd/migrate/main.go` (replace with full dispatcher)
- Test: `backend/cmd/migrate/main_test.go` (create)
- Modify: `AGENTS.md` (update the `cmd/migrate` usage line)

**Interfaces:**
- Consumes: `db.Migrate`, `db.MigrateDown`, `db.Version`, `db.Force`, `db.Steps`, `db.Goto`, `db.ErrNoMigrations` (Task 1); `config.Load()`.
- Produces: a `migrator` interface + `run(...) int` entrypoint (internal to the package; tested via a fake).

- [ ] **Step 1: Write the failing tests**

Create `backend/cmd/migrate/main_test.go`:

```go
package main

import (
	"bytes"
	"strings"
	"testing"
)

// fakeMigrator records calls and returns canned results.
type fakeMigrator struct {
	calls   []string
	version uint
	dirty   bool
	applied bool
	err     error
}

func (f *fakeMigrator) Up() error      { f.calls = append(f.calls, "Up"); return f.err }
func (f *fakeMigrator) DownAll() error { f.calls = append(f.calls, "DownAll"); return f.err }
func (f *fakeMigrator) Version() (uint, bool, bool, error) {
	f.calls = append(f.calls, "Version")
	return f.version, f.dirty, f.applied, f.err
}
func (f *fakeMigrator) Force(v int) error  { f.calls = append(f.calls, "Force"); return f.err }
func (f *fakeMigrator) Steps(n int) error  { f.calls = append(f.calls, "Steps"); return f.err }
func (f *fakeMigrator) Goto(v uint) error  { f.calls = append(f.calls, "Goto"); return f.err }

func runWith(args []string, isTTY bool, stdin string, m migrator) (int, string, string) {
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut, strings.NewReader(stdin), isTTY, m)
	return code, out.String(), errOut.String()
}

func TestUpRoutesToUp(t *testing.T) {
	m := &fakeMigrator{}
	code, _, _ := runWith([]string{"up"}, false, "", m)
	if code != 0 || len(m.calls) != 1 || m.calls[0] != "Up" {
		t.Fatalf("up: code=%d calls=%v", code, m.calls)
	}
}

func TestUnknownSubcommandIsUsageError(t *testing.T) {
	m := &fakeMigrator{}
	code, _, errOut := runWith([]string{"frobnicate"}, false, "", m)
	if code != 2 {
		t.Fatalf("unknown subcommand: code=%d, want 2", code)
	}
	if !strings.Contains(errOut, "usage") {
		t.Fatalf("expected usage on stderr, got %q", errOut)
	}
	if len(m.calls) != 0 {
		t.Fatalf("expected no migrator calls, got %v", m.calls)
	}
}

func TestNoArgsIsUsageError(t *testing.T) {
	code, _, _ := runWith([]string{}, false, "", &fakeMigrator{})
	if code != 2 {
		t.Fatalf("no args: code=%d, want 2", code)
	}
}

func TestStepsRequiresInteger(t *testing.T) {
	m := &fakeMigrator{}
	code, _, _ := runWith([]string{"steps", "abc"}, false, "", m)
	if code != 2 || len(m.calls) != 0 {
		t.Fatalf("steps abc: code=%d calls=%v, want code 2 / no calls", code, m.calls)
	}
}

func TestForceRequiresArg(t *testing.T) {
	code, _, _ := runWith([]string{"force"}, false, "", &fakeMigrator{})
	if code != 2 {
		t.Fatalf("force without arg: code=%d, want 2", code)
	}
}

func TestVersionAppliedOutput(t *testing.T) {
	m := &fakeMigrator{version: 2, applied: true}
	code, out, _ := runWith([]string{"version"}, false, "", m)
	if code != 0 || !strings.Contains(out, "version: 2") || !strings.Contains(out, "clean") {
		t.Fatalf("version: code=%d out=%q", code, out)
	}
}

func TestVersionNotAppliedOutput(t *testing.T) {
	m := &fakeMigrator{applied: false}
	code, out, _ := runWith([]string{"version"}, false, "", m)
	if code != 0 || !strings.Contains(out, "no migrations applied") {
		t.Fatalf("version (none): code=%d out=%q", code, out)
	}
}

func TestStepsForwardNeedsNoConfirm(t *testing.T) {
	m := &fakeMigrator{}
	code, _, _ := runWith([]string{"steps", "1"}, true, "", m) // TTY but forward = not destructive
	if code != 0 || len(m.calls) != 1 || m.calls[0] != "Steps" {
		t.Fatalf("steps 1: code=%d calls=%v", code, m.calls)
	}
}

func TestDestructiveAbortsOnTTYNo(t *testing.T) {
	m := &fakeMigrator{}
	code, _, _ := runWith([]string{"down"}, true, "n\n", m) // TTY, answer no
	if code == 0 {
		t.Fatalf("declined down should be non-zero, got 0")
	}
	for _, c := range m.calls {
		if c == "DownAll" {
			t.Fatalf("DownAll must not run when declined; calls=%v", m.calls)
		}
	}
}

func TestDestructiveProceedsOnTTYYes(t *testing.T) {
	m := &fakeMigrator{}
	code, _, _ := runWith([]string{"down"}, true, "y\n", m)
	if code != 0 || len(m.calls) != 1 || m.calls[0] != "DownAll" {
		t.Fatalf("confirmed down: code=%d calls=%v", code, m.calls)
	}
}

func TestDestructiveNonTTYProceedsUnattended(t *testing.T) {
	m := &fakeMigrator{}
	code, _, _ := runWith([]string{"down"}, false, "", m) // no TTY = CI, proceed
	if code != 0 || len(m.calls) != 1 || m.calls[0] != "DownAll" {
		t.Fatalf("non-tty down: code=%d calls=%v", code, m.calls)
	}
}

func TestYesFlagSkipsConfirm(t *testing.T) {
	m := &fakeMigrator{}
	code, _, _ := runWith([]string{"force", "2", "--yes"}, true, "", m)
	if code != 0 || len(m.calls) == 0 || m.calls[len(m.calls)-1] != "Force" {
		t.Fatalf("force --yes: code=%d calls=%v", code, m.calls)
	}
}

func TestGotoDownIsDestructive(t *testing.T) {
	m := &fakeMigrator{version: 2, applied: true} // current=2; goto 1 reverts
	code, _, _ := runWith([]string{"goto", "1"}, true, "n\n", m)
	if code == 0 {
		t.Fatalf("declined goto-down should be non-zero, got 0")
	}
	for _, c := range m.calls {
		if c == "Goto" {
			t.Fatalf("Goto must not run when declined; calls=%v", m.calls)
		}
	}
}

func TestGotoUpNeedsNoConfirm(t *testing.T) {
	m := &fakeMigrator{version: 1, applied: true} // current=1; goto 2 is forward
	code, _, _ := runWith([]string{"goto", "2"}, true, "", m)
	if code != 0 || m.calls[len(m.calls)-1] != "Goto" {
		t.Fatalf("goto up: code=%d calls=%v", code, m.calls)
	}
}

func TestRuntimeErrorExitsOne(t *testing.T) {
	m := &fakeMigrator{err: errOops}
	code, _, errOut := runWith([]string{"up"}, false, "", m)
	if code != 1 {
		t.Fatalf("runtime error: code=%d, want 1", code)
	}
	if !strings.Contains(errOut, "oops") {
		t.Fatalf("expected error on stderr, got %q", errOut)
	}
}
```

Add this sentinel at the bottom of the test file:

```go
var errOops = stringError("oops")

type stringError string

func (e stringError) Error() string { return string(e) }
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/migrate/ -v`
Expected: compile failure — `run`, `migrator` undefined.

- [ ] **Step 3: Implement the full CLI**

Replace `backend/cmd/migrate/main.go` entirely:

```go
// Command migrate manages database schema migrations. It reads DATABASE_URL
// and reuses the embedded migrations via internal/db — no external migrate CLI.
//
//	migrate up                 apply all pending migrations
//	migrate down               revert ALL migrations
//	migrate version            print current version and dirty state
//	migrate force <v>          set version to <v>, clearing a dirty state
//	migrate steps <n>          apply <n> (or revert -<n>) migrations
//	migrate goto <v>           migrate to version <v> (0 reverts all)
//
// Destructive operations prompt for confirmation when stdin is a terminal;
// pass -y/--yes to skip, or run non-interactively (CI) to proceed unattended.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/stroem/shopping-list/backend/internal/config"
	"github.com/stroem/shopping-list/backend/internal/db"
)

const usage = `usage: migrate <command> [arg] [-y|--yes]

commands:
  up             apply all pending migrations
  down           revert ALL migrations (destructive)
  version        print current schema version and dirty state
  force <v>      set version to <v>, clearing a dirty state (destructive)
  steps <n>      apply <n>, or revert when <n> is negative
  goto <v>       migrate to version <v> (0 reverts all)

flags:
  -y, --yes      skip the confirmation prompt for destructive operations
  -h, --help     show this help
`

// migrator is the migration backend run depends on. The real implementation
// wraps internal/db; tests substitute a fake.
type migrator interface {
	Up() error
	DownAll() error
	Version() (version uint, dirty bool, applied bool, err error)
	Force(v int) error
	Steps(n int) error
	Goto(v uint) error
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}
	fi, _ := os.Stdin.Stat()
	isTTY := fi != nil && fi.Mode()&os.ModeCharDevice != 0
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Stdin, isTTY, dbMigrator{url: cfg.DatabaseURL}))
}

// run parses args, gates destructive operations, invokes m and returns an exit
// code: 0 success, 2 usage error, 1 runtime failure or declined confirmation.
func run(args []string, stdout, stderr io.Writer, stdin io.Reader, isTTY bool, m migrator) int {
	var yes bool
	var positional []string
	for _, a := range args {
		switch a {
		case "-y", "--yes":
			yes = true
		case "-h", "--help":
			fmt.Fprint(stdout, usage)
			return 0
		default:
			positional = append(positional, a)
		}
	}
	if len(positional) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}

	cmd := positional[0]
	arg := positional[1:]

	switch cmd {
	case "up":
		return exec(stdout, stderr, "migrations applied", m.Up)
	case "down":
		if !confirm("revert ALL migrations", stdout, stdin, isTTY, yes) {
			fmt.Fprintln(stderr, "aborted")
			return 1
		}
		return exec(stdout, stderr, "migrations reverted", m.DownAll)
	case "version":
		return printVersion(stdout, stderr, m)
	case "force":
		v, ok := intArg(stderr, "force", arg)
		if !ok {
			return 2
		}
		if !confirm(fmt.Sprintf("force version to %d", v), stdout, stdin, isTTY, yes) {
			fmt.Fprintln(stderr, "aborted")
			return 1
		}
		return exec(stdout, stderr, fmt.Sprintf("forced to version %d", v), func() error { return m.Force(v) })
	case "steps":
		n, ok := intArg(stderr, "steps", arg)
		if !ok {
			return 2
		}
		if n < 0 && !confirm(fmt.Sprintf("revert %d migration(s)", -n), stdout, stdin, isTTY, yes) {
			fmt.Fprintln(stderr, "aborted")
			return 1
		}
		return exec(stdout, stderr, "steps applied", func() error { return m.Steps(n) })
	case "goto":
		v, ok := intArg(stderr, "goto", arg)
		if !ok {
			return 2
		}
		if v < 0 {
			fmt.Fprintln(stderr, "goto: version must be >= 0")
			return 2
		}
		target := uint(v)
		if reverts, code := gotoReverts(stderr, m, target); code != 0 {
			return code
		} else if reverts && !confirm(fmt.Sprintf("migrate down to version %d", target), stdout, stdin, isTTY, yes) {
			fmt.Fprintln(stderr, "aborted")
			return 1
		}
		return exec(stdout, stderr, fmt.Sprintf("migrated to version %d", target), func() error { return m.Goto(target) })
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", cmd, usage)
		return 2
	}
}

// gotoReverts reports whether migrating to target would revert (target below
// the current applied version). The int is a non-zero exit code on failure.
func gotoReverts(stderr io.Writer, m migrator, target uint) (bool, int) {
	cur, _, applied, err := m.Version()
	if err != nil {
		fmt.Fprintf(stderr, "read version: %v\n", err)
		return false, 1
	}
	return applied && target < cur, 0
}

func printVersion(stdout, stderr io.Writer, m migrator) int {
	v, dirty, applied, err := m.Version()
	if err != nil {
		fmt.Fprintf(stderr, "version: %v\n", err)
		return 1
	}
	if !applied {
		fmt.Fprintln(stdout, "no migrations applied")
		return 0
	}
	state := "clean"
	if dirty {
		state = "dirty"
	}
	fmt.Fprintf(stdout, "version: %d (%s)\n", v, state)
	return 0
}

// exec runs op, prints okMsg to stdout on success, and maps a runtime error to
// exit code 1 with the message on stderr.
func exec(stdout, stderr io.Writer, okMsg string, op func() error) int {
	if err := op(); err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, okMsg)
	return 0
}

// intArg parses a single required integer argument for cmd.
func intArg(stderr io.Writer, cmd string, arg []string) (int, bool) {
	if len(arg) != 1 {
		fmt.Fprintf(stderr, "%s: expects exactly one integer argument\n", cmd)
		return 0, false
	}
	n, err := strconv.Atoi(arg[0])
	if err != nil {
		fmt.Fprintf(stderr, "%s: %q is not an integer\n", cmd, arg[0])
		return 0, false
	}
	return n, true
}

// confirm returns true when the destructive op may proceed: --yes set, no TTY
// (unattended/CI), or an interactive y/yes answer.
func confirm(action string, stdout io.Writer, stdin io.Reader, isTTY, yes bool) bool {
	if yes || !isTTY {
		return true
	}
	fmt.Fprintf(stdout, "%s — proceed? [y/N]: ", action)
	line, _ := bufio.NewReader(stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

// dbMigrator is the production migrator backed by internal/db.
type dbMigrator struct{ url string }

func (d dbMigrator) Up() error      { return db.Migrate(context.Background(), d.url) }
func (d dbMigrator) DownAll() error { return db.MigrateDown(d.url) }
func (d dbMigrator) Version() (uint, bool, bool, error) {
	v, dirty, err := db.Version(d.url)
	if errors.Is(err, db.ErrNoMigrations) {
		return 0, false, false, nil
	}
	if err != nil {
		return 0, false, false, err
	}
	return v, dirty, true, nil
}
func (d dbMigrator) Force(v int) error { return db.Force(d.url, v) }
func (d dbMigrator) Steps(n int) error { return db.Steps(d.url, n) }
func (d dbMigrator) Goto(v uint) error { return db.Goto(d.url, v) }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/migrate/ -v`
Expected: PASS (all dispatch/usage/confirmation/version tests; no DB needed).

- [ ] **Step 5: Build + vet the whole backend**

Run: `go build ./... && go vet ./...`
Expected: no output, exit 0.

- [ ] **Step 6: Update AGENTS.md**

In `AGENTS.md`, replace the line:

```
- `go run ./cmd/migrate up|down` — apply/revert migrations (reads `DATABASE_URL`).
```

with:

```
- `go run ./cmd/migrate <up|down|version|force <v>|steps <n>|goto <v>>` — migration CLI (reads `DATABASE_URL`; destructive ops prompt on a TTY, `-y` to skip).
```

- [ ] **Step 7: Commit**

```bash
git add cmd/migrate/main.go cmd/migrate/main_test.go ../AGENTS.md
git commit -m "feat(migrate): add version/force/steps/goto subcommands

Grow cmd/migrate into a full CLI with TTY-aware confirmation for
destructive operations, stdout/stderr split, and non-zero exits on
misuse. Dispatch is unit-tested via a fake migrator (no DB required).

Closes #25"
```

---

## Self-Review

**Spec coverage:**
- `version`/`force`/`steps`/`goto` subcommands → Task 2 dispatch + Task 1 runners. ✓
- Reads `DATABASE_URL`, clear help, non-zero exits on misuse → `main()` config load, `usage` const, exit `2` paths. ✓
- Reuses embedded migrations + `internal/db`, no external CLI dep → Task 1 wrappers; `cmd/migrate` imports only `internal/db`+`config`. ✓
- TTY-aware confirmation, `--yes`, stdlib TTY detection → `confirm`, `main()` `os.ModeCharDevice`. ✓
- stdout/stderr split, exit codes 0/1/2 → `exec`, `printVersion`, usage paths. ✓
- `down` = revert all, behind confirm → `down` case calls `m.DownAll()` after `confirm`. ✓
- `goto 0` = full revert → `db.Goto` `version==0` → `m.Down()`. ✓
- Deploy-path `migrate up` non-interactive → `up` never confirms; non-TTY proceeds. ✓
- Doc update → Task 2 Step 6. ✓

**Placeholder scan:** none — all steps contain real code/commands.

**Type consistency:** `migrator` interface methods match `fakeMigrator` and `dbMigrator` implementations; `db.Version` returns `(uint, bool, error)` consistently across Task 1 (definition) and Task 2 (`dbMigrator.Version` consumption); `ErrNoMigrations` defined in Task 1, consumed in Task 2. ✓
