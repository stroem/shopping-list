// Package db owns database schema migration. Query/repository code lives with
// each domain package, not here.
package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/stroem/shopping-list/backend/migrations"
)

// Migrate applies all pending up migrations to the database at databaseURL.
// It is safe to call on every startup: an already-migrated database is a no-op.
// databaseURL must use the pgx scheme, e.g. "pgx5://user:pass@host:5432/db".
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
	m, err := migrate.NewWithSourceInstance("iofs", src, databaseURL)
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
func MigrateDown(databaseURL string) error {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("load embedded migrations: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, databaseURL)
	if err != nil {
		return fmt.Errorf("init migrator: %w", err)
	}
	defer m.Close()
	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("revert migrations: %w", err)
	}
	return nil
}
