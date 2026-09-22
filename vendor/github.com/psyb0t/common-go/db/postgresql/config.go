package postgresql

import (
	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/gonfiguration"
)

const (
	envVarNameDBPostgresqlPort = "DB_POSTGRESQL_PORT"
	defaultPostgresqlPort      = 5432
)

type Config struct {
	Hostname string `env:"DB_POSTGRESQL_HOSTNAME"`
	Port     int    `env:"DB_POSTGRESQL_PORT"`
	Username string `env:"DB_POSTGRESQL_USERNAME"`
	Password string `env:"DB_POSTGRESQL_PASSWORD"`
	Database string `env:"DB_POSTGRESQL_DATABASE"`
	IsSSL    bool   `env:"DB_POSTGRESQL_ISSSL"`
}

func (c *Config) validate() error {
	if c.Hostname == "" {
		return ctxerrors.Wrap(ErrHostnameRequired, "hostname")
	}

	if c.Port == 0 {
		return ctxerrors.Wrap(ErrPortRequired, "port")
	}

	if c.Username == "" {
		return ctxerrors.Wrap(ErrUsernameRequired, "username")
	}

	if c.Password == "" {
		return ctxerrors.Wrap(ErrPasswordRequired, "password")
	}

	if c.Database == "" {
		return ctxerrors.Wrap(ErrDatabaseRequired, "database")
	}

	return nil
}

func parseConfig() (Config, error) {
	cfg := Config{}

	gonfiguration.SetDefault(
		envVarNameDBPostgresqlPort,
		defaultPostgresqlPort,
	)

	if err := gonfiguration.Parse(&cfg); err != nil {
		return Config{}, ctxerrors.Wrap(err, "could not parse config")
	}

	return cfg, nil
}
