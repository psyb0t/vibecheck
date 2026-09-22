//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/psyb0t/vibecheck/tests/testinfra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
)

// buildJSONRequest is shared by the tests that need a request the standard
// client helper cannot express, such as one bound to a cancellable context.
func buildJSONRequest(
	t *testing.T,
	ctx context.Context,
	url string,
	body any,
) *http.Request {
	t.Helper()

	encoded, err := json.Marshal(body)
	require.NoError(t, err, "encode request body")

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, url, bytes.NewReader(encoded),
	)
	require.NoError(t, err, "build request")

	req.Header.Set(headerContentType, contentTypeJSON)

	return req
}

// sharedPostgres is started at most once per run.
//
// Bringing up a server costs several seconds and the tests that need one do
// not interfere with each other: they read what they just wrote and filter
// by their own rows. Starting one per test made the suite miss the
// coverage target's wall-clock budget for no extra signal.
var (
	sharedPostgres     testcontainers.Container
	sharedPostgresOnce sync.Once
	sharedPostgresErr  error
)

func postgresContainer(t *testing.T) testcontainers.Container {
	t.Helper()

	sharedPostgresOnce.Do(func() {
		sharedPostgres, sharedPostgresErr = testinfra.StartPostgres(
			context.Background(),
		)
	})

	require.NoError(t, sharedPostgresErr, "start postgres")
	require.NotNil(t, sharedPostgres, "postgres container")

	return sharedPostgres
}

// startPostgresApp brings up an app wired to the shared Postgres server.
func startPostgresApp(t *testing.T) *testinfra.App {
	t.Helper()

	ctx := context.Background()

	postgres := postgresContainer(t)

	dbPort, err := testinfra.PostgresPort(ctx, postgres)
	require.NoError(t, err, "resolve the postgres port")

	providerPort, err := testinfra.FreePort()
	require.NoError(t, err, "reserve a provider port")

	provider, err := testinfra.StartFakeProvider(providerPort, defaultReply())
	require.NoError(t, err, "start the fake provider")

	t.Cleanup(func() { _ = provider.Close() }) //nolint:errcheck // cleanup

	app, err := testinfra.StartApp(ctx, testinfra.AppConfig{
		Driver:          testinfra.DriverPostgres,
		Policies:        map[string][]byte{policyFileName: firewallPolicy()},
		ProviderBaseURL: provider.URL(),
		HostPorts:       []int{providerPort, dbPort},
	}, postgres)
	require.NoError(t, err, "start the app against postgres")

	t.Cleanup(func() { app.Terminate(context.Background()) })

	return app
}

// PostgreSQL is a first-class target, not a theoretical one. Running the
// same behaviors against it is what catches a migration or a query that
// only happens to work on SQLite.
func TestPostgresServesTheSameBehaviorAsSQLite(t *testing.T) {
	app := startPostgresApp(t)
	api := newClient(app)

	t.Run("readiness reports the database answers", func(t *testing.T) {
		res := api.do(t, http.MethodGet, "/ready", nil, nil)
		assert.Equal(t, http.StatusOK, res.Status)
	})

	t.Run("an evaluation round trips", func(t *testing.T) {
		created := api.evaluate(
			t, evaluateBody(allowingFacts(), "postgres fixture"), nil,
		)
		assert.Equal(t, "allow", created["outcome"])

		id, ok := created["id"].(string)
		require.True(t, ok)

		res := api.do(t, http.MethodGet, "/v1/evaluations/"+id, nil, nil)
		require.Equal(t, http.StatusOK, res.Status)
		assert.Equal(t, created["id"], res.Map(t)["id"])
	})

	t.Run("the pre-rule floor holds", func(t *testing.T) {
		created := api.evaluate(
			t, evaluateBody(blockingFacts(), "postgres pre-rule"), nil,
		)
		assert.Equal(t, "block", created["outcome"])
		assert.Equal(t, "pre_rule", created["executionPath"])
	})

	t.Run("pagination spans pages", func(t *testing.T) {
		const total = 5

		for i := range total {
			api.evaluate(t, evaluateBody(
				allowingFacts(), "pg page "+strconv.Itoa(i),
			), nil)
		}

		res := api.do(
			t, http.MethodGet, "/v1/evaluations?limit=2", nil, nil,
		)
		require.Equal(t, http.StatusOK, res.Status)

		page := res.Map(t)
		items, ok := page["items"].([]any)
		require.True(t, ok)
		assert.Len(t, items, 2)
		assert.Equal(t, true, page["hasMore"])
	})

	t.Run("MCP works against postgres too", func(t *testing.T) {
		result, isError := api.callTool(t, toolEvaluate, map[string]any{
			"policyName":    policyName,
			"policyVersion": policyVersion,
			"state":         "postgres over mcp",
			"facts":         allowingFacts(),
		})
		require.False(t, isError)
		assert.Equal(t, "allow", result["outcome"])
	})
}

// Migrations run on every start. Starting a second process against an
// already-migrated database must be a no-op, not a failure.
func TestMigrationsAreIdempotentAcrossRestarts(t *testing.T) {
	ctx := context.Background()

	postgres := postgresContainer(t)

	dbPort, err := testinfra.PostgresPort(ctx, postgres)
	require.NoError(t, err)

	providerPort, err := testinfra.FreePort()
	require.NoError(t, err)

	provider, err := testinfra.StartFakeProvider(providerPort, defaultReply())
	require.NoError(t, err)

	t.Cleanup(func() { _ = provider.Close() }) //nolint:errcheck // cleanup

	config := testinfra.AppConfig{
		Driver:          testinfra.DriverPostgres,
		Policies:        map[string][]byte{policyFileName: firewallPolicy()},
		ProviderBaseURL: provider.URL(),
		HostPorts:       []int{providerPort, dbPort},
	}

	first, err := testinfra.StartApp(ctx, config, postgres)
	require.NoError(t, err, "the first start runs the migrations")

	created := newClient(first).evaluate(
		t, evaluateBody(allowingFacts(), "survives a restart"), nil,
	)
	id, ok := created["id"].(string)
	require.True(t, ok)

	first.Terminate(ctx)

	second, err := testinfra.StartApp(ctx, config, postgres)
	require.NoError(
		t, err,
		"a second start against a migrated database must come up clean",
	)

	t.Cleanup(func() { second.Terminate(context.Background()) })

	res := newClient(second).do(
		t, http.MethodGet, "/v1/evaluations/"+id, nil, nil,
	)
	require.Equal(t, http.StatusOK, res.Status)
	assert.Equal(
		t, id, res.Map(t)["id"],
		"the row written before the restart is still there",
	)
}

// An idempotency key makes a retried request replay the original decision
// instead of billing a second provider call.
func TestIdempotentReplayReturnsTheOriginalDecision(t *testing.T) {
	app, provider := startApp(t, testinfra.AppConfig{})
	api := newClient(app)

	provider.Reset()

	key := uuid.NewString()
	body := evaluateBody(allowingFacts(), "idempotency fixture")
	headers := map[string]string{headerIdempotencyKey: key}

	first := api.evaluate(t, body, headers)
	callsAfterFirst := provider.CallCount()
	require.Equal(t, 1, callsAfterFirst, "the first call reaches the provider")

	second := api.evaluate(t, body, headers)

	assert.Equal(
		t, first["id"], second["id"],
		"a replay returns the original evaluation, not a new one",
	)
	assert.Equal(t, first["outcome"], second["outcome"])
	assert.Equal(t, first["createdAt"], second["createdAt"])

	assert.Equal(
		t, callsAfterFirst, provider.CallCount(),
		"a replay must not bill a second provider call",
	)
}

// Reusing a key with different content is a caller bug. Silently returning
// the old answer would hide it; silently computing a new one would break the
// key's meaning.
func TestReusingAnIdempotencyKeyWithADifferentRequestConflicts(t *testing.T) {
	app, _ := startApp(t, testinfra.AppConfig{})
	api := newClient(app)

	key := uuid.NewString()
	headers := map[string]string{headerIdempotencyKey: key}

	api.evaluate(t, evaluateBody(allowingFacts(), "original state"), headers)

	res := api.do(t, http.MethodPost, "/v1/evaluations",
		evaluateBody(allowingFacts(), "a different state"), headers)

	require.Equal(
		t, http.StatusConflict, res.Status, string(res.Body),
	)
	assert.NotEmpty(t, res.ErrorCode(t))
}

func TestDifferentIdempotencyKeysProduceDifferentEvaluations(t *testing.T) {
	app, _ := startApp(t, testinfra.AppConfig{})
	api := newClient(app)

	body := evaluateBody(allowingFacts(), "same body, different keys")

	first := api.evaluate(t, body, map[string]string{
		headerIdempotencyKey: uuid.NewString(),
	})
	second := api.evaluate(t, body, map[string]string{
		headerIdempotencyKey: uuid.NewString(),
	})

	assert.NotEqual(
		t, first["id"], second["id"],
		"a distinct key is a distinct request",
	)
}

// Retention is what keeps an audit log from growing without bound. The loop
// has to actually delete, and it has to leave fresh rows alone.
func TestRetentionCleanupRemovesExpiredEvaluations(t *testing.T) {
	app, _ := startApp(t, testinfra.AppConfig{
		Env: map[string]string{
			// Everything older than a second is expired, swept every
			// second, so the loop runs several times inside the test.
			"VIBECHECK_EVALUATION_RETENTION":       "1s",
			"VIBECHECK_RETENTION_CLEANUP_INTERVAL": "1s",
		},
	})

	api := newClient(app)

	created := api.evaluate(
		t, evaluateBody(allowingFacts(), "retention fixture"), nil,
	)
	id, ok := created["id"].(string)
	require.True(t, ok)

	res := api.do(t, http.MethodGet, "/v1/evaluations/"+id, nil, nil)
	require.Equal(
		t, http.StatusOK, res.Status, "the row exists before cleanup",
	)

	require.Eventually(t, func() bool {
		probe := api.do(t, http.MethodGet, "/v1/evaluations/"+id, nil, nil)

		return probe.Status == http.StatusNotFound
	}, 30*time.Second, time.Second,
		"the retention loop must eventually delete the expired evaluation")

	scrape := scrapeMetrics(t, app)
	assert.Contains(
		t, scrape, "vibecheck_retention_runs_total",
		"a retention pass is observable in the metrics",
	)
}

// A deployment that keeps evaluations forever is a valid configuration, and
// the cleanup loop must not delete under it.
func TestRetentionLeavesRowsAloneWhenTheWindowIsZero(t *testing.T) {
	app, _ := startApp(t, testinfra.AppConfig{
		Env: map[string]string{
			"VIBECHECK_EVALUATION_RETENTION":       "0s",
			"VIBECHECK_RETENTION_CLEANUP_INTERVAL": "1s",
		},
	})

	api := newClient(app)

	created := api.evaluate(
		t, evaluateBody(allowingFacts(), "kept forever"), nil,
	)
	id, ok := created["id"].(string)
	require.True(t, ok)

	// Long enough for several cleanup passes to have run.
	time.Sleep(4 * time.Second)

	res := api.do(t, http.MethodGet, "/v1/evaluations/"+id, nil, nil)
	assert.Equal(
		t, http.StatusOK, res.Status,
		"a zero retention window keeps evaluations indefinitely",
	)
}

// scrapeMetrics reads the internal listener's Prometheus output.
func scrapeMetrics(t *testing.T, app *testinfra.App) string {
	t.Helper()

	req, err := http.NewRequestWithContext(
		context.Background(), http.MethodGet, app.MetricsURL, nil,
	)
	require.NoError(t, err)

	res, err := testinfra.HTTPClient().Do(req)
	require.NoError(t, err, "scrape metrics")

	defer func() { _ = res.Body.Close() }() //nolint:errcheck // read side

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err, "read the metrics body")

	return string(body)
}
