package db_test

import (
	"path/filepath"
	"testing"

	"github.com/psyb0t/vibecheck/internal/pkg/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type observerSpy struct {
	operation string
	failed    bool
	seconds   float64
}

func (o *observerSpy) ObserveDB(
	operation string,
	failed bool,
	seconds float64,
) {
	o.operation = operation
	o.failed = failed
	o.seconds = seconds
}

func TestOpenWiresDatabaseOperationMetrics(t *testing.T) {
	t.Parallel()

	observer := &observerSpy{}
	database, err := db.Open(
		t.Context(),
		db.Config{
			Driver:     db.DriverSQLite,
			SQLitePath: filepath.Join(t.TempDir(), "metrics.sqlite"),
		},
		db.WithObserver(observer),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })

	result := database.Gorm.Exec("SELECT 1")
	require.NoError(t, result.Error)

	assert.Equal(t, "raw", observer.operation)
	assert.False(t, observer.failed)
	assert.GreaterOrEqual(t, observer.seconds, float64(0))
}
