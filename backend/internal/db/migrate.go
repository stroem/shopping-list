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

// Migrate applies all pending up migrations to the database at databaseURL.
// It is safe to call on every startup: an already-migrated database is a no-op.
// databaseURL is a standard postgres:// URL (normalized to pgx5:// internally).
//
// ctx is part of the intended call-site interface (cmd/api passes its startup
// context); golang-migrate v4's Up() does not yet thread a context, so
// cancellation cannot currently propagate into the migration run.
func Migrate(ctx context.Context, databaseURL string) error {
	_ = ctx
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("load embedded migrations: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, migrateURL(databaseURL))
	if err != nil {
		return fmt.Errorf("init migrator: %w", err)
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
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("load embedded migrations: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, migrateURL(databaseURL))
	if err != nil {
		return fmt.Errorf("init migrator: %w", err)
	}
	defer m.Close()
	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("revert migrations: %w", err)
	}
	return nil
}
