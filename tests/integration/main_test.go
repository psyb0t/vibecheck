//go:build integration

// Package integration drives Vibecheck through its real production image.
//
// Every test here talks to a container built from the repo Dockerfile over
// real HTTP, with a faithful System One fake standing in for the paid
// provider. Nothing in this package inspects Vibecheck's source, reaches
// into its process, or substitutes a mock for a boundary the service crosses
// in production.
package integration

import (
	"context"
	"os"
	"testing"

	"github.com/psyb0t/vibecheck/tests/testinfra"
)

// sharedApp is the default deployment most tests run against: SQLite, no
// auth, inline policies off, one loaded policy, one fake provider.
//
// It is built once because building the production image and booting it per
// test would put the suite into the tens of minutes. Tests that need a
// different deployment start their own app.
var (
	sharedApp      *testinfra.App
	sharedProvider *testinfra.FakeProvider
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	code, err := runSuite(ctx, m)
	if err != nil {
		// The harness failed before any test could run, so there is no
		// *testing.T to report through.
		os.Stderr.WriteString("integration setup failed: " + err.Error() + "\n")
		os.Exit(1)
	}

	os.Exit(code)
}

func runSuite(ctx context.Context, m *testing.M) (int, error) {
	port, err := testinfra.FreePort()
	if err != nil {
		return 0, err
	}

	provider, err := testinfra.StartFakeProvider(port, defaultReply())
	if err != nil {
		return 0, err
	}

	defer func() { _ = provider.Close() }() //nolint:errcheck // shutting down

	app, err := testinfra.StartApp(ctx, testinfra.AppConfig{
		Policies:        map[string][]byte{policyFileName: firewallPolicy()},
		ProviderBaseURL: provider.URL(),
		HostPorts:       []int{port},
	}, nil)
	if err != nil {
		return 0, err
	}

	defer app.Terminate(context.Background())

	// The Postgres container is started lazily by the tests that need one.
	// Whether it exists is only known after the run, so it is torn down
	// here rather than by a t.Cleanup on whichever test happened to be
	// first.
	defer terminateSharedPostgres()

	sharedApp = app
	sharedProvider = provider

	return m.Run(), nil
}

func terminateSharedPostgres() {
	if sharedPostgres == nil {
		return
	}

	_ = sharedPostgres.Terminate( //nolint:errcheck // teardown is advisory
		context.Background(),
	)
}
