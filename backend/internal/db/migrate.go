// Package db owns database schema migration. Query/repository code lives with
// each domain package, not here.
package db

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/stroem/shopping-list/backend/migrations"
)

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

// Migrate applies all pending up migrations to the database at databaseURL.
// It is safe to call on every startup: an already-migrated database is a no-op.
// databaseURL is a standard postgres:// URL (normalized to pgx5:// internally).
//
// ctx is part of the intended call-site interface (cmd/api passes its startup
// context); golang-migrate v4's Up() does not yet thread a context, so
// cancellation cannot currently propagate into the migration run.
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

// MigrateDown reverts all migrations. Intended for tests; not called in production.
// databaseURL is a standard postgres:// URL (normalized to pgx5:// internally).
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
