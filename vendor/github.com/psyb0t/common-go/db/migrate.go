// Package db provides database helpers shared by the supported SQL dialects.
package db

import (
	"embed"
	"errors"
	"log/slog"
	"net/url"
	"os"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database"
	_ "github.com/golang-migrate/migrate/v4/source/file" // Registers file:// migration sources.
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxerrors/commerr"
)

const embeddedMigrationSourceName = "iofs"

// MigrateUp applies every pending migration from either the embedded filesystem
// or a filesystem path. The caller retains ownership of migrationDriver.
func MigrateUp(
	databaseName string,
	migrationDriver database.Driver,
	path string,
	migrationFS *embed.FS,
) error {
	migrator, err := newMigrator(databaseName, migrationDriver, path, migrationFS)
	if err != nil {
		return ctxerrors.Wrap(err, "create migrator")
	}

	if err := migrator.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			slog.Info("database is already up to date", "database", databaseName)

			return nil
		}

		return ctxerrors.Wrap(err, "migrate up")
	}

	return nil
}

// MigrateDown reverts the requested number of migrations from either the
// embedded filesystem or a filesystem path. The caller retains ownership of
// migrationDriver.
func MigrateDown(
	databaseName string,
	migrationDriver database.Driver,
	path string,
	migrationFS *embed.FS,
	steps int,
) error {
	if steps <= 0 {
		return ctxerrors.Wrap(commerr.ErrInvalidArgument, "migration steps must be greater than zero")
	}

	migrator, err := newMigrator(databaseName, migrationDriver, path, migrationFS)
	if err != nil {
		return ctxerrors.Wrap(err, "create migrator")
	}

	if err := migrator.Steps(-steps); err != nil {
		return ctxerrors.Wrap(err, "migrate down")
	}

	return nil
}

// MigrateForce marks the database at version without running a migration. The
// caller retains ownership of migrationDriver.
func MigrateForce(
	databaseName string,
	migrationDriver database.Driver,
	path string,
	migrationFS *embed.FS,
	version int,
) error {
	migrator, err := newMigrator(databaseName, migrationDriver, path, migrationFS)
	if err != nil {
		return ctxerrors.Wrap(err, "create migrator")
	}

	if err := migrator.Force(version); err != nil {
		return ctxerrors.Wrap(err, "force migration version")
	}

	return nil
}

func newMigrator(
	databaseName string,
	migrationDriver database.Driver,
	path string,
	migrationFS *embed.FS,
) (*migrate.Migrate, error) {
	if migrationDriver == nil {
		return nil, ctxerrors.Wrap(commerr.ErrRequiredFieldNotSet, "migration database driver")
	}

	if migrationFS != nil {
		sourceDriver, err := iofs.New(migrationFS, path)
		if err != nil {
			return nil, ctxerrors.Wrap(err, "create embedded migration source")
		}

		migrator, err := migrate.NewWithInstance(
			embeddedMigrationSourceName,
			sourceDriver,
			databaseName,
			migrationDriver,
		)
		if err != nil {
			return nil, ctxerrors.Wrap(err, "create migration instance")
		}

		return migrator, nil
	}

	if path == "" {
		return nil, ctxerrors.Wrap(commerr.ErrEmptyMigrationsPath, "migration filesystem path")
	}

	if _, err := os.Stat(path); err != nil {
		return nil, ctxerrors.Wrap(err, "validate migration filesystem path")
	}

	migrationURL := url.URL{Scheme: "file", Path: path}

	migrator, err := migrate.NewWithDatabaseInstance(
		migrationURL.String(),
		databaseName,
		migrationDriver,
	)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "create migration instance")
	}

	return migrator, nil
}
