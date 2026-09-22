// Package db opens Vibecheck's database and runs its migrations.
//
// Two dialects are supported. SQLite is the default and targets one
// self-hosted process; PostgreSQL targets shared or replicated deployments.
// Everything above this package works against the generated repositories and
// does not know which one is in use.
package db

import (
	"context"
	"database/sql"
	"strconv"
	"time"

	"github.com/glebarez/sqlite"
	commondb "github.com/psyb0t/common-go/db"
	"github.com/psyb0t/common-go/db/postgresql"
	commonsqlite "github.com/psyb0t/common-go/db/sqlite"
	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxerrors/commerr"
	"github.com/psyb0t/ctxscope"
	"github.com/psyb0t/vibecheck/internal/pkg/db/migrations"
	"gorm.io/gorm"
)

// Driver selects the dialect.
type Driver string

const (
	DriverSQLite   Driver = "sqlite"
	DriverPostgres Driver = "postgres"
)

func (d Driver) String() string {
	return string(d)
}

func (d Driver) IsValid() bool {
	switch d {
	case DriverSQLite, DriverPostgres:
		return true
	}

	return false
}

// SQLite pragmas. WAL keeps a reader from blocking the single writer, and the
// busy timeout is what turns a concurrent-write collision into a short wait
// instead of an immediate SQLITE_BUSY error.
const (
	defaultBusyTimeout = 5 * time.Second

	pragmaJournalWAL     = "PRAGMA journal_mode=WAL"
	pragmaForeignKeysOn  = "PRAGMA foreign_keys=ON"
	pragmaSynchronousNRM = "PRAGMA synchronous=NORMAL"
)

// SQLite is limited to one writer regardless of pool size. Allowing more open
// connections just moves contention from the application to the file lock.
const sqliteMaxOpenConns = 1

// Database is an open connection plus the handles the rest of the process
// needs.
type Database struct {
	Driver Driver
	Gorm   *gorm.DB
	SQL    *sql.DB

	close func() error
}

// Observer receives bounded operation measurements. SQL text and arguments
// never cross this boundary.
type Observer interface {
	ObserveDB(operation string, failed bool, seconds float64)
}

type openOptions struct {
	observer Observer
}

// OpenOption adjusts database instrumentation without changing connection
// semantics.
type OpenOption func(*openOptions)

// WithObserver records GORM operation counts and durations.
func WithObserver(observer Observer) OpenOption {
	return func(options *openOptions) {
		options.observer = observer
	}
}

// Close releases the connection. It is safe to call more than once.
func (d *Database) Close() error {
	if d == nil || d.close == nil {
		return nil
	}

	closeFn := d.close
	d.close = nil

	if err := closeFn(); err != nil {
		return ctxerrors.Wrap(err, "close database")
	}

	return nil
}

// Config selects and configures the dialect.
type Config struct {
	Driver Driver

	// SQLitePath is the database file. It must sit on the one writable
	// path the production image mounts.
	SQLitePath string

	// SQLiteBusyTimeout is how long a blocked writer waits before failing.
	SQLiteBusyTimeout time.Duration

	// Postgres holds the connection settings the common-go driver needs.
	Postgres PostgresConfig
}

// PostgresConfig is the subset of connection settings Vibecheck exposes.
type PostgresConfig struct {
	Hostname string
	Port     int
	Username string
	Password string
	Database string
	IsSSL    bool
}

// Open connects and runs migrations up to the latest version.
//
// Migrations run on every start. They are the only thing that creates or
// alters the schema; no model ever auto-migrates.
func Open(
	ctx context.Context,
	config Config,
	options ...OpenOption,
) (*Database, error) {
	settings := openOptions{}
	for _, option := range options {
		option(&settings)
	}

	if !config.Driver.IsValid() {
		return nil, ctxerrors.Wrapf(
			commerr.ErrInvalidValue,
			"unsupported database driver %q",
			config.Driver,
		)
	}

	if config.Driver == DriverSQLite {
		return openSQLite(ctx, config, settings)
	}

	return openPostgres(ctx, config, settings)
}

func openSQLite(
	ctx context.Context,
	config Config,
	settings openOptions,
) (*Database, error) {
	if config.SQLitePath == "" {
		return nil, ctxerrors.Wrap(
			commerr.ErrRequiredConfigValueNotSet, "sqlite database path",
		)
	}

	busyTimeout := config.SQLiteBusyTimeout
	if busyTimeout <= 0 {
		busyTimeout = defaultBusyTimeout
	}

	gormDB, err := gorm.Open(
		sqlite.Open(config.SQLitePath),
		&gorm.Config{Logger: commondb.NewGormSlogLogger()},
	)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "open sqlite database")
	}

	sqlDB, err := gormDB.DB()
	if err != nil {
		return nil, ctxerrors.Wrap(err, "take sqlite connection pool")
	}

	sqlDB.SetMaxOpenConns(sqliteMaxOpenConns)

	if err := registerObserver(gormDB, settings.observer); err != nil {
		return nil, closeOnSetupError(sqlDB, err, "register sqlite metrics")
	}

	if err := applySQLitePragmas(ctx, sqlDB, busyTimeout); err != nil {
		return nil, err
	}

	if err := commonsqlite.MigrateUp(
		sqlDB, migrations.SQLitePath, &migrations.FS,
	); err != nil {
		return nil, ctxerrors.Wrap(err, "migrate sqlite database")
	}

	ctxscope.GetLogger(ctx).Info(
		"database ready", "driver", DriverSQLite.String(),
	)

	return &Database{
		Driver: DriverSQLite,
		Gorm:   gormDB,
		SQL:    sqlDB,
		close:  sqlDB.Close,
	}, nil
}

func applySQLitePragmas(
	ctx context.Context,
	sqlDB *sql.DB,
	busyTimeout time.Duration,
) error {
	pragmas := []string{
		pragmaJournalWAL,
		pragmaForeignKeysOn,
		pragmaSynchronousNRM,
		busyTimeoutPragma(busyTimeout),
	}

	for _, pragma := range pragmas {
		if _, err := sqlDB.ExecContext(ctx, pragma); err != nil {
			return ctxerrors.Wrapf(err, "apply %q", pragma)
		}
	}

	return nil
}

func busyTimeoutPragma(timeout time.Duration) string {
	return "PRAGMA busy_timeout=" +
		strconv.FormatInt(timeout.Milliseconds(), 10)
}

func openPostgres(
	ctx context.Context,
	config Config,
	settings openOptions,
) (*Database, error) {
	// ensureDBExists stays false: creating a database is a deployment
	// action, and letting the service do it means a typo in the name
	// silently starts a brand new empty database instead of failing.
	const ensureDBExists = false

	connection, err := postgresql.NewWithConfig(
		ctx,
		postgresql.Config{
			Hostname: config.Postgres.Hostname,
			Port:     config.Postgres.Port,
			Username: config.Postgres.Username,
			Password: config.Postgres.Password,
			Database: config.Postgres.Database,
			IsSSL:    config.Postgres.IsSSL,
		},
		ensureDBExists,
	)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "open postgres database")
	}

	err = registerObserver(connection.GormDB, settings.observer)
	if err != nil {
		closeErr := connection.Close()
		if closeErr != nil {
			return nil, ctxerrors.Wrap(
				err,
				"register postgres metrics, and the connection failed to close",
			)
		}

		return nil, ctxerrors.Wrap(err, "register postgres metrics")
	}

	if err := connection.MigrateUp(
		migrations.PostgresPath, &migrations.FS,
	); err != nil {
		closeErr := connection.Close()
		if closeErr != nil {
			return nil, ctxerrors.Wrap(err, "migrate postgres database, and the connection also failed to close") //nolint:lll // one error string, splitting it would change the text
		}

		return nil, ctxerrors.Wrap(err, "migrate postgres database")
	}

	ctxscope.GetLogger(ctx).Info(
		"database ready", "driver", DriverPostgres.String(),
	)

	return &Database{
		Driver: DriverPostgres,
		Gorm:   connection.GormDB,
		SQL:    connection.SQLDB,
		close:  connection.Close,
	}, nil
}

func closeOnSetupError(
	sqlDB *sql.DB,
	cause error,
	operation string,
) error {
	if err := sqlDB.Close(); err != nil {
		return ctxerrors.Wrap(
			cause, operation+", and the connection failed to close",
		)
	}

	return ctxerrors.Wrap(cause, operation)
}
