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
