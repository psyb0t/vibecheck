package postgresql

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/psyb0t/common-go/db"
	"github.com/psyb0t/ctxerrors"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type Postgresql struct {
	config Config
	SQLDB  *sql.DB
	GormDB *gorm.DB
}

func New(
	ctx context.Context,
	ensureDBExists bool,
) (*Postgresql, error) {
	cfg, err := parseConfig()
	if err != nil {
		return nil, ctxerrors.Wrap(err, "could not parse config")
	}

	return NewWithConfig(ctx, cfg, ensureDBExists)
}

func NewWithConfig(
	ctx context.Context,
	cfg Config,
	ensureDBExists bool,
) (*Postgresql, error) {
	if err := cfg.validate(); err != nil {
		return nil, ctxerrors.Wrap(err, "could not validate config")
	}

	p := &Postgresql{
		config: cfg,
	}

	if ensureDBExists {
		if err := ensureDatabaseExists(ctx, cfg); err != nil {
			return nil, ctxerrors.Wrap(err, "failed to ensure database exists")
		}
	}

	slog.Debug("setting up sql database connection",
		"database", cfg.Database,
		"hostname", cfg.Hostname,
		"port", cfg.Port,
	)

	var err error

	p.SQLDB, err = connectToDB(ctx, getDSN(cfg, false))
	if err != nil {
		return nil, ctxerrors.Wrap(err, "could not connect to database")
	}

	slog.Debug("setting up gorm database")

	p.GormDB, err = gorm.Open(postgres.New(postgres.Config{
		Conn: p.SQLDB,
	}), &gorm.Config{
		Logger: db.NewGormSlogLogger(),
	})
	if err != nil {
		return nil, ctxerrors.Wrap(err, "could not open gorm database")
	}

	return p, nil
}

const maxDBConnectElapsedTime = 30 * time.Second

func connectToDB(
	ctx context.Context,
	dsn string,
) (*sql.DB, error) {
	sqlDB, err := backoff.Retry(ctx, func() (*sql.DB, error) {
		sqlDB, err := sql.Open(db.DriverNamePGX, dsn)
		if err != nil {
			return nil, ctxerrors.Wrap(err, "could not open database")
		}

		if err = sqlDB.PingContext(ctx); err != nil {
			return nil, ctxerrors.Wrap(err, "could not ping database")
		}

		return sqlDB, nil
	}, backoff.WithMaxElapsedTime(maxDBConnectElapsedTime))
	if err != nil {
		return nil, ctxerrors.Wrap(err, "could not connect to database")
	}

	return sqlDB, nil
}

func ensureDatabaseExists(
	ctx context.Context,
	cfg Config,
) error {
	slog.Debug("checking if database exists",
		"database", cfg.Database,
		"hostname", cfg.Hostname,
		"port", cfg.Port,
	)

	dsn := getDSN(cfg, true)

	sqlDB, err := connectToDB(ctx, dsn)
	if err != nil {
		return ctxerrors.Wrap(err, "could not connect to database")
	}

	defer func() {
		if err := sqlDB.Close(); err != nil {
			slog.Error("failed to close temporary database connection", "error", err)
		}
	}()

	exists, err := databaseExists(ctx, sqlDB, cfg.Database)
	if err != nil {
		return ctxerrors.Wrap(err, "could not check if database exists")
	}

	if exists {
		slog.Debug("database already exists", "database", cfg.Database)

		return nil
	}

	slog.Info("database does not exist, creating it", "database", cfg.Database)

	if err = createDatabase(ctx, sqlDB, cfg.Database); err != nil {
		return ctxerrors.Wrap(err, "could not create database")
	}

	slog.Info("database created successfully", "database", cfg.Database)

	return nil
}

func (p *Postgresql) Close() error {
	if err := p.SQLDB.Close(); err != nil {
		return ctxerrors.Wrap(err, "could not close sql database")
	}

	return nil
}

func getDSN(cfg Config, usePostgresDatabase bool) string {
	sslMode := "disable"
	if cfg.IsSSL {
		sslMode = "require"
	}

	dbName := cfg.Database
	if usePostgresDatabase {
		dbName = db.DBNamePostgres
	}

	return fmt.Sprintf(
		"host=%s user=%s password=%s dbname=%s port=%d sslmode=%s",
		cfg.Hostname,
		cfg.Username,
		cfg.Password,
		dbName,
		cfg.Port,
		sslMode,
	)
}

func databaseExists(
	ctx context.Context,
	sqlDB *sql.DB,
	dbName string,
) (bool, error) {
	var exists bool

	query := `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`

	err := sqlDB.QueryRowContext(ctx, query, dbName).Scan(&exists)
	if err != nil {
		dbNotExistsErrMsg := fmt.Sprintf(
			`pq: database "%s" does not exist`,
			dbName,
		)

		if err.Error() == dbNotExistsErrMsg {
			return false, nil
		}

		return false, ctxerrors.Wrap(err, "could not check if database exists")
	}

	return exists, nil
}

func createDatabase(
	ctx context.Context,
	sqlDB *sql.DB,
	dbName string,
) error {
	escapedName := strings.ReplaceAll(dbName, `"`, `""`)
	query := fmt.Sprintf(`CREATE DATABASE "%s"`, escapedName)

	_, err := sqlDB.ExecContext(ctx, query)
	if err != nil {
		return ctxerrors.Wrap(err, "could not create database")
	}

	return nil
}
