package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"sync/atomic"

	"github.com/golang-migrate/migrate/v4/database"
	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxerrors/commerr"
)

const (
	createMigrationTableStatement = `
CREATE TABLE IF NOT EXISTS schema_migrations (version uint64, dirty bool);
CREATE UNIQUE INDEX IF NOT EXISTS version_unique ON schema_migrations (version);
`
	deleteMigrationVersionStatement = `DELETE FROM schema_migrations`
	selectMigrationVersionStatement = `SELECT version, dirty FROM schema_migrations LIMIT 1`
	insertMigrationVersionStatement = `
INSERT INTO schema_migrations (version, dirty)
VALUES (?, ?)`
)

// driver adapts a caller-owned SQLite connection for golang-migrate without
// registering or selecting a database/sql driver.
type driver struct {
	sqlDB *sql.DB

	locked atomic.Bool
}

func newDriver(sqlDB *sql.DB) (*driver, error) {
	driver := &driver{sqlDB: sqlDB}
	if err := driver.ensureMigrationTable(); err != nil {
		return nil, ctxerrors.Wrap(err, "ensure migration table")
	}

	return driver, nil
}

//nolint:ireturn // database.Driver requires an interface return for Open.
func (driver *driver) Open(string) (database.Driver, error) {
	return nil, ctxerrors.Wrap(
		commerr.ErrUnsupported,
		"open SQLite migration database",
	)
}

func (driver *driver) Close() error {
	return nil
}

func (driver *driver) Lock() error {
	if !driver.locked.CompareAndSwap(false, true) {
		return database.ErrLocked
	}

	return nil
}

func (driver *driver) Unlock() error {
	if !driver.locked.CompareAndSwap(true, false) {
		return database.ErrNotLocked
	}

	return nil
}

func (driver *driver) Run(migration io.Reader) error {
	contents, err := io.ReadAll(migration)
	if err != nil {
		return ctxerrors.Wrap(err, "read migration")
	}

	return driver.execute(string(contents))
}

func (driver *driver) SetVersion(version int, dirty bool) error {
	transaction, err := driver.sqlDB.BeginTx(context.Background(), nil)
	if err != nil {
		return &database.Error{OrigErr: err, Err: "start migration version transaction"}
	}

	if _, err := transaction.ExecContext(
		context.Background(),
		deleteMigrationVersionStatement,
	); err != nil {
		return driver.rollbackWithError(transaction, err, deleteMigrationVersionStatement)
	}

	if version >= 0 || (version == database.NilVersion && dirty) {
		if _, err := transaction.ExecContext(
			context.Background(),
			insertMigrationVersionStatement,
			version,
			dirty,
		); err != nil {
			return driver.rollbackWithError(
				transaction,
				err,
				insertMigrationVersionStatement,
			)
		}
	}

	if err := transaction.Commit(); err != nil {
		return &database.Error{OrigErr: err, Err: "commit migration version transaction"}
	}

	return nil
}

func (driver *driver) Version() (int, bool, error) {
	var version int

	var dirty bool
	if err := driver.sqlDB.QueryRowContext(
		context.Background(),
		selectMigrationVersionStatement,
	).Scan(
		&version,
		&dirty,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return database.NilVersion, false, nil
		}

		return database.NilVersion, false, &database.Error{
			OrigErr: err,
			Query:   []byte(selectMigrationVersionStatement),
		}
	}

	return version, dirty, nil
}

func (driver *driver) Drop() error {
	return ctxerrors.Wrap(commerr.ErrUnsupported, "drop SQLite database")
}

func (driver *driver) ensureMigrationTable() error {
	if err := driver.Lock(); err != nil {
		return ctxerrors.Wrap(err, "lock migration driver")
	}

	_, executionErr := driver.sqlDB.ExecContext(
		context.Background(),
		createMigrationTableStatement,
	)

	unlockErr := driver.Unlock()
	if executionErr != nil {
		if unlockErr != nil {
			executionErr = errors.Join(executionErr, unlockErr)
		}

		return &database.Error{
			OrigErr: executionErr,
			Query:   []byte(createMigrationTableStatement),
		}
	}

	if unlockErr != nil {
		return ctxerrors.Wrap(unlockErr, "unlock migration driver")
	}

	return nil
}

func (driver *driver) execute(query string) error {
	transaction, err := driver.sqlDB.BeginTx(context.Background(), nil)
	if err != nil {
		return &database.Error{OrigErr: err, Err: "start migration transaction"}
	}

	if _, err := transaction.ExecContext(context.Background(), query); err != nil {
		return driver.rollbackWithError(transaction, err, query)
	}

	if err := transaction.Commit(); err != nil {
		return &database.Error{OrigErr: err, Err: "commit migration transaction"}
	}

	return nil
}

func (driver *driver) rollbackWithError(
	transaction *sql.Tx,
	originalErr error,
	query string,
) error {
	if err := transaction.Rollback(); err != nil {
		originalErr = errors.Join(originalErr, err)
	}

	return &database.Error{OrigErr: originalErr, Query: []byte(query)}
}
