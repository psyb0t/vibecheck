// Package sqlite provides SQLite migration helpers backed by golang-migrate.
package sqlite

import (
	"context"
	"database/sql"
	"embed"

	commondb "github.com/psyb0t/common-go/db"
	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxerrors/commerr"
)

const migrationDatabaseName = "sqlite"

// MigrateUp applies every pending migration. sqlDB remains owned by the caller
// and stays open after the migration completes.
func MigrateUp(sqlDB *sql.DB, path string, migrationFS *embed.FS) error {
	driver, err := migrationDriver(sqlDB)
	if err != nil {
		return ctxerrors.Wrap(err, "create SQLite migration driver")
	}

	if err := commondb.MigrateUp(migrationDatabaseName, driver, path, migrationFS); err != nil {
		return ctxerrors.Wrap(err, "migrate SQLite up")
	}

	return nil
}

// MigrateDown reverts the requested number of migrations. sqlDB remains owned
// by the caller and stays open after the migration completes.
func MigrateDown(
	sqlDB *sql.DB,
	path string,
	steps int,
	migrationFS *embed.FS,
) error {
	driver, err := migrationDriver(sqlDB)
	if err != nil {
		return ctxerrors.Wrap(err, "create SQLite migration driver")
	}

	if err := commondb.MigrateDown(
		migrationDatabaseName,
		driver,
		path,
		migrationFS,
		steps,
	); err != nil {
		return ctxerrors.Wrap(err, "migrate SQLite down")
	}

	return nil
}

// MigrateForce marks sqlDB at version without running a migration. sqlDB
// remains owned by the caller and stays open after the operation completes.
func MigrateForce(
	sqlDB *sql.DB,
	path string,
	version int,
	migrationFS *embed.FS,
) error {
	driver, err := migrationDriver(sqlDB)
	if err != nil {
		return ctxerrors.Wrap(err, "create SQLite migration driver")
	}

	if err := commondb.MigrateForce(
		migrationDatabaseName,
		driver,
		path,
		migrationFS,
		version,
	); err != nil {
		return ctxerrors.Wrap(err, "force SQLite migration version")
	}

	return nil
}

func migrationDriver(sqlDB *sql.DB) (*driver, error) {
	if sqlDB == nil {
		return nil, ctxerrors.Wrap(commerr.ErrRequiredFieldNotSet, "SQLite database")
	}

	if err := sqlDB.PingContext(context.Background()); err != nil {
		return nil, ctxerrors.Wrap(err, "ping SQLite database")
	}

	driver, err := newDriver(sqlDB)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "create SQLite migration driver")
	}

	return driver, nil
}
