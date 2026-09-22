// Package migrations embeds Vibecheck's schema.
//
// The binary is the migrations. There is no directory to mount and no way for
// the SQL to drift from the code that runs it.
//
// The two dialects have separate trees because their column types differ:
// PostgreSQL wants TIMESTAMPTZ and BYTEA, SQLite wants DATETIME and BLOB. The
// table and column names are identical, so the generated repositories and
// every query work unchanged against either.
package migrations

import "embed"

// Subdirectory names inside FS, passed to the common-go migration runner as
// its path argument.
const (
	PostgresPath = "postgres"
	SQLitePath   = "sqlite"
)

//go:embed postgres/*.sql sqlite/*.sql
var FS embed.FS
