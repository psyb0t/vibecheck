package testinfra

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxerrors/commerr"
	"github.com/psyb0t/ctxscope"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Ports the app image exposes.
const (
	appHTTPPort    = "8080"
	appMetricsPort = "9091"
)

// Container paths matching the production image's layout.
const (
	appPolicyDir  = "/config/policies"
	appSQLitePath = "/data/vibecheck.sqlite"
)

// readyLog is what the service logs once both listeners accept.
//
// It is a stronger start signal than the framework's "running app", which
// fires before the database is open, so waiting on it removes the race where
// a test's first request beats migrations.
const readyLog = "service ready"

const (
	appStartTimeout = 5 * time.Minute
	dbStartTimeout  = 2 * time.Minute
)

// The fixed name the suite builds the production image under, so repeated
// StartApp calls reuse one build instead of producing a UUID-tagged image
// each time.
const (
	appImageRepo = "vibecheck-test"
	appImageTag  = "api-suite"

	// A coverage run builds a second, instrumented image. It gets its own
	// tag so an instrumented binary can never be mistaken for the one the
	// ordinary suite asserts against.
	appImageTagCover = "api-suite-cover"
)

// coverDirEnvVar is where the framework's coverage target tells the test
// process to drop out-of-process counters. When it is set, the suite builds
// the app image with coverage on and mounts that directory into the
// container as GOCOVERDIR, so the service's own lines count toward the gate
// instead of reading as dead code.
const coverDirEnvVar = "SERVICEPACK_COVDATA_DIR"

// containerCoverDir is where the covdata directory is mounted inside the
// app container.
const containerCoverDir = "/covdata"

// coverModePerm is wide on purpose: the test process and the container's
// non-root user are different UIDs and both have to write here.
const coverModePerm = 0o777

// coverOtherWrite is the write bit the container's user relies on when the
// directory belongs to somebody else.
const coverOtherWrite = 0o002

// coveredModule is the module path the instrumented build counts.
const coveredModule = "github.com/psyb0t/vibecheck"

// hostGateway is the name testcontainers gives the host inside a container
// when ContainerRequest.HostAccessPorts is set. It is how the app container
// reaches a fake provider running in the test process.
const hostGateway = "host.testcontainers.internal"

// The app reaches Postgres over a shared Docker network rather than through
// the host tunnel. The tunnel forwards to the test process, and the mapped
// database port lives on the daemon's host instead, so a DIND run cannot
// reach it that way. A user-defined network gives both containers a stable
// DNS name and takes the host out of the path entirely.
const (
	testNetworkPrefix  = "vibecheck-test-net"
	postgresHostPrefix = "vibecheck-postgres"
)

// testRunID is one identity per test process, used to suffix the network and
// the database hostname.
//
// Both used to be fixed strings. A Postgres container left over from an
// earlier run stayed attached to that network under the same alias, Docker
// round-robins DNS across every container sharing an alias, and two app
// containers in one test then reached two different databases. The second
// one reported its schema as already migrated while the tables it needed
// were somewhere else.
//
//nolint:gochecknoglobals // process identity, computed once, read-only after
var testRunID = sync.OnceValue(func() string {
	buf := make([]byte, testRunIDBytes)
	if _, err := rand.Read(buf); err != nil {
		return strconv.Itoa(os.Getpid())
	}

	return hex.EncodeToString(buf)
})

// testRunIDBytes is enough entropy that two concurrent runs never collide.
const testRunIDBytes = 6

// testNetworkName is the per-process shared network.
func testNetworkName() string {
	return testNetworkPrefix + "-" + testRunID()
}

// postgresHostname is the per-process DNS name the app reaches Postgres by.
func postgresHostname() string {
	return postgresHostPrefix + "-" + testRunID()
}

// postgresImage is digest-pinned so a test run cannot silently move to a
// different server version and change migration behavior underneath us.
const postgresImage = "postgres:17-alpine@sha256:" +
	"b0f9560a2de083e2cc7382e75f808c7381a32852a7ec49117deedb300e552b24"

// postgresReadyLog appears once while the image initializes its data
// directory and again when the real server starts, so waits look for the
// second occurrence and never connect to the init-only instance.
const postgresReadyLog = "database system is ready to accept connections"

// postgresReadyLogCount is the occurrence to wait for: the second.
const postgresReadyLogCount = 2

// Postgres credentials for the throwaway test database. Fake by
// construction: the container lives for one test run and is never published.
const (
	PostgresUser     = "vibecheck"
	PostgresPassword = "vibecheck-test-password"
	PostgresDatabase = "vibecheck"
	postgresPort     = "5432"
)

// Driver values a test picks between.
const (
	DriverSQLite   = "sqlite"
	DriverPostgres = "postgres"
)

// AppConfig is what a test wants to vary about the service under test.
type AppConfig struct {
	// Driver selects sqlite or postgres. Empty means sqlite.
	Driver string

	// APIToken enables bearer auth when non-empty.
	APIToken string

	// AllowInlinePolicies turns on caller-supplied policy documents.
	AllowInlinePolicies bool

	// ProviderBaseURL points the service at a fake provider. Build it with
	// ContainerURLForHostPort so the container can actually reach it.
	ProviderBaseURL string

	// Policies are policy documents to place in the container's policy
	// directory, keyed by file name.
	//
	// They are copied into the image rather than bind-mounted because the
	// suite runs under DIND, where a path the test process created is not
	// a path the Docker daemon can resolve.
	Policies map[string][]byte

	// StoreInputs and DataKey turn on encrypted input retention.
	StoreInputs bool
	DataKey     string

	// Env adds or overrides any other VIBECHECK_ variable, for the limit
	// and retention knobs a test exercises directly.
	Env map[string]string

	// HostPorts are host ports the container must reach as hostGateway.
	// Every fake server port and the Postgres mapped port goes here.
	HostPorts []int
}

// App is a running service container plus the URLs a test talks to.
type App struct {
	Container testcontainers.Container

	// BaseURL is the public listener, including scheme and mapped port.
	BaseURL string

	// MetricsURL is the internal listener's Prometheus endpoint.
	MetricsURL string
}

// ensureNetwork creates the shared test network if it is not there yet.
//
// testcontainers does not create a network named in ContainerRequest.Networks.
// It looks the name up and silently skips the attachment when the lookup
// fails, which would leave the app on the bridge with no route to Postgres
// and no error to explain it.
func ensureNetwork(ctx context.Context) error {
	// The network.New replacement generates its own name and hands back a
	// *DockerNetwork to pass into each container request. Pinning a known
	// name still requires this NetworkRequest, so migrating would move the
	// deprecation rather than remove it. Revisit when the harness can
	// thread one network value through every StartX call.
	//nolint:staticcheck // see above: the replacement cannot name a network
	_, err := testcontainers.GenericNetwork(ctx,
		testcontainers.GenericNetworkRequest{
			NetworkRequest: testcontainers.NetworkRequest{
				Name:       testNetworkName(),
				Attachable: true,
			},
		},
	)
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return ctxerrors.Wrap(err, "create the shared test network")
	}

	return nil
}

// StartPostgres brings up a throwaway PostgreSQL server on the shared test
// network, reachable from the app container as postgresHostname().
func StartPostgres(ctx context.Context) (testcontainers.Container, error) {
	if err := ensureNetwork(ctx); err != nil {
		return nil, err
	}

	req := testcontainers.ContainerRequest{
		Image:        postgresImage,
		ExposedPorts: []string{postgresPort + "/tcp"},
		Networks:     []string{testNetworkName()},
		NetworkAliases: map[string][]string{
			testNetworkName(): {postgresHostname()},
		},
		Env: map[string]string{
			"POSTGRES_USER":     PostgresUser,
			"POSTGRES_PASSWORD": PostgresPassword,
			"POSTGRES_DB":       PostgresDatabase,
		},
		WaitingFor: wait.ForAll(
			wait.ForLog(postgresReadyLog).WithOccurrence(postgresReadyLogCount),
			wait.ForListeningPort(postgresPort+"/tcp"),
		).WithDeadline(dbStartTimeout),
	}

	instance, err := testcontainers.GenericContainer(ctx,
		testcontainers.GenericContainerRequest{
			ContainerRequest: req,
			Started:          true,
		},
	)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "start postgres")
	}

	return instance, nil
}

// StartApp builds the production image and runs the service from it.
//
// The image is the real Dockerfile, not a test-only build, so anything the
// image gets wrong (a missing writable path, a root-owned binary, a broken
// entrypoint) fails a test instead of hiding behind `go run`.
func StartApp(
	ctx context.Context,
	config AppConfig,
	postgres testcontainers.Container,
) (*App, error) {
	env, err := appEnv(ctx, config, postgres)
	if err != nil {
		return nil, err
	}

	root, err := repoRoot()
	if err != nil {
		return nil, err
	}

	coverDir, err := prepareCoverDir(ctx)
	if err != nil {
		return nil, err
	}

	instance, err := testcontainers.GenericContainer(ctx,
		testcontainers.GenericContainerRequest{
			ContainerRequest: appRequest(config, env, root, coverDir),
			Started:          true,
		},
	)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "build and start the app image")
	}

	return describeApp(ctx, instance)
}

// appRequest builds the container request for one app.
//
// It is separate from StartApp so the coverage plumbing can be asserted
// without a Docker daemon. That plumbing silently went dead once already:
// the helpers existed, nothing called them, and the coverage run reported
// the service packages as untested rather than failing.
//
// coverDir empty means an ordinary run, which must use the plain production
// image with no instrumentation.
func appRequest(
	config AppConfig,
	env map[string]string,
	root string,
	coverDir string,
) testcontainers.ContainerRequest {
	return testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    root,
			Dockerfile: appDockerfile,
			// Repo and Tag default to a fresh UUID per call, which makes
			// every StartApp rebuild the whole production image from
			// scratch. Pinning the name lets the daemon's layer cache do
			// its job, so the suite builds once instead of once per test.
			Repo:      appImageRepo,
			Tag:       imageTag(coverDir),
			BuildArgs: buildArgs(coverDir),
			KeepImage: true,
		},
		Cmd: []string{appRunCommand},
		Env: env,
		ExposedPorts: []string{
			appHTTPPort + "/tcp",
			appMetricsPort + "/tcp",
		},
		HostAccessPorts:    config.HostPorts,
		Files:              policyFiles(config.Policies),
		Networks:           appNetworks(config),
		HostConfigModifier: mountCoverDir(coverDir),
		WaitingFor: wait.ForLog(readyLog).
			WithStartupTimeout(appStartTimeout),
	}
}

// appNetworks puts the app on the shared network only when it needs to
// reach Postgres, so a SQLite run stays on the default bridge.
func appNetworks(config AppConfig) []string {
	if config.Driver != DriverPostgres {
		return nil
	}

	return []string{testNetworkName()}
}

// policyFiles turns the test's documents into container file copies.
//
// The mode is read-only for the running user: a Vibecheck process must never
// be able to rewrite the policies it is enforcing.
func policyFiles(policies map[string][]byte) []testcontainers.ContainerFile {
	const readOnlyMode = 0o444

	files := make([]testcontainers.ContainerFile, 0, len(policies))
	for name, body := range policies {
		files = append(files, testcontainers.ContainerFile{
			Reader:            bytes.NewReader(body),
			ContainerFilePath: path.Join(appPolicyDir, name),
			FileMode:          readOnlyMode,
		})
	}

	return files
}

// describeApp resolves the mapped host ports into usable URLs.
func describeApp(
	ctx context.Context,
	instance testcontainers.Container,
) (*App, error) {
	host, err := instance.Host(ctx)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "resolve app container host")
	}

	httpPort, err := instance.MappedPort(ctx, appHTTPPort+"/tcp")
	if err != nil {
		return nil, ctxerrors.Wrap(err, "resolve app http port")
	}

	metricsPort, err := instance.MappedPort(ctx, appMetricsPort+"/tcp")
	if err != nil {
		return nil, ctxerrors.Wrap(err, "resolve app metrics port")
	}

	metricsBase := "http://" + net.JoinHostPort(host, metricsPort.Port())

	return &App{
		Container:  instance,
		BaseURL:    "http://" + net.JoinHostPort(host, httpPort.Port()),
		MetricsURL: metricsBase + "/metrics",
	}, nil
}

// Terminate stops the app container. Best effort, safe on a nil receiver.
func (a *App) Terminate(ctx context.Context) {
	if a == nil || a.Container == nil {
		return
	}

	_ = a.Container.Terminate(ctx)
}

// Logs returns everything the app container has written so far, which is how
// a test asserts on structured log output such as secret redaction.
func (a *App) Logs(ctx context.Context) (string, error) {
	reader, err := a.Container.Logs(ctx)
	if err != nil {
		return "", ctxerrors.Wrap(err, "read app container logs")
	}

	defer func() { _ = reader.Close() }()

	body, err := io.ReadAll(reader)
	if err != nil {
		return "", ctxerrors.Wrap(err, "drain app container logs")
	}

	return string(body), nil
}

// appEnv builds the container environment from the test's intent.
func appEnv(
	_ context.Context,
	config AppConfig,
	postgres testcontainers.Container,
) (map[string]string, error) {
	env := map[string]string{
		"LOG_LEVEL":                  "info",
		"LOG_FORMAT":                 "json",
		"VIBECHECK_POLICY_DIR":       appPolicyDir,
		"VIBECHECK_TYPESAFE_API_KEY": "test-provider-key",
		"VIBECHECK_DB_DRIVER":        DriverSQLite,
		"VIBECHECK_DB_SQLITE_PATH":   appSQLitePath,
	}

	if config.APIToken != "" {
		env["VIBECHECK_API_TOKEN"] = config.APIToken
	}

	if config.AllowInlinePolicies {
		env["VIBECHECK_ALLOW_INLINE_POLICIES"] = "true"
	}

	if config.ProviderBaseURL != "" {
		env["VIBECHECK_TYPESAFE_BASE_URL"] = config.ProviderBaseURL
	}

	if config.StoreInputs {
		env["VIBECHECK_STORE_INPUTS"] = "true"
		env["VIBECHECK_DATA_KEY"] = config.DataKey
	}

	if config.Driver == DriverPostgres {
		if err := applyPostgresEnv(env, postgres); err != nil {
			return nil, err
		}
	}

	if os.Getenv(coverDirEnvVar) != "" {
		env["GOCOVERDIR"] = containerCoverDir
	}

	maps.Copy(env, config.Env)

	return env, nil
}

// prepareCoverDir returns the host covdata directory for a coverage run, or
// an empty string when coverage is off.
//
// The path must be absolute. It goes to the Docker daemon as a bind source
// and the daemon resolves it on the host, not in this process, so a relative
// path would quietly mount somewhere else and the counters would vanish.
func prepareCoverDir(ctx context.Context) (string, error) {
	raw := os.Getenv(coverDirEnvVar)
	if raw == "" {
		return "", nil
	}

	dir := filepath.Clean(raw)
	if !filepath.IsAbs(dir) {
		return "", ctxerrors.Wrapf(
			commerr.ErrInvalidValue,
			"%s must be an absolute path, got %q", coverDirEnvVar, raw,
		)
	}

	if err := os.MkdirAll(dir, coverModePerm); err != nil {
		return "", ctxerrors.Wrap(err, "create the covdata directory")
	}

	if err := widenCoverDir(ctx, dir); err != nil {
		return "", err
	}

	return dir, nil
}

// widenCoverDir makes sure the container's non-root user can write counters.
//
// MkdirAll honours umask, so the mode usually has to be set again. The chmod
// can legitimately fail when the directory already exists and belongs to
// another user, which happens whenever a previous run created it under a
// different UID. That is only fatal if the directory is also not
// world-writable, because then the container writes nothing and coverage
// silently reads as zero, which is worse than a failed run.
func widenCoverDir(ctx context.Context, dir string) error {
	chmodErr := os.Chmod(dir, coverModePerm)
	if chmodErr == nil {
		return nil
	}

	info, statErr := os.Stat(dir)
	if statErr != nil {
		return ctxerrors.Wrap(statErr, "stat the covdata directory")
	}

	if info.Mode().Perm()&coverOtherWrite != 0 {
		return nil
	}

	// The directory exists, belongs to somebody else, and is not writable
	// by the container user. Recreating it is safe: the only thing it ever
	// holds is this run's counters, and the alternative is a run that looks
	// green while every containerised package reads as dead code.
	recreateErr := recreateCoverDir(dir)
	if recreateErr == nil {
		return nil
	}

	// Recreating is best effort, so its failure is reported rather than
	// returned: the caller needs the verdict on the directory that is
	// actually there, and chmodErr is the cause worth surfacing.
	ctxscope.GetLogger(ctx).Warn(
		"could not recreate the covdata directory",
		"err", recreateErr,
		"dir", dir,
		"reason", "covdata_recreate_failed",
	)

	return coverDirVerdict(dir, info.Mode().Perm(), chmodErr)
}

// recreateCoverDir replaces a covdata directory this process cannot widen.
func recreateCoverDir(dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return ctxerrors.Wrap(err, "remove the foreign covdata directory")
	}

	if err := os.MkdirAll(dir, coverModePerm); err != nil {
		return ctxerrors.Wrap(err, "recreate the covdata directory")
	}

	if err := os.Chmod(dir, coverModePerm); err != nil {
		return ctxerrors.Wrap(err, "widen the recreated covdata directory")
	}

	return nil
}

// coverDirVerdict decides whether a failed chmod is survivable.
//
// It is split out from widenCoverDir because the interesting branch cannot
// be reached by a test running as root, where chmod never fails.
func coverDirVerdict(dir string, perm os.FileMode, chmodErr error) error {
	if perm&coverOtherWrite != 0 {
		return nil
	}

	return ctxerrors.Wrapf(
		chmodErr,
		"the covdata directory %s is mode %#o and not writable by the "+
			"container user, so coverage would silently be empty",
		dir, perm,
	)
}

// imageTag keeps the instrumented build under its own tag.
func imageTag(coverDir string) string {
	if coverDir == "" {
		return appImageTag
	}

	return appImageTagCover
}

// buildArgs turns coverage on in the image build for a coverage run.
func buildArgs(coverDir string) map[string]*string {
	if coverDir == "" {
		return nil
	}

	cover := "-cover -coverpkg=" + coveredModule + "/..."

	return map[string]*string{"BUILD_COVER": &cover}
}

// mountCoverDir bind-mounts the host covdata directory into the container.
//
// The DIND runner mounts the workspace at its own host path, so the path the
// test process sees is a path the Docker daemon can resolve too.
func mountCoverDir(coverDir string) func(*container.HostConfig) {
	if coverDir == "" {
		return nil
	}

	return func(hostConfig *container.HostConfig) {
		hostConfig.Binds = append(
			hostConfig.Binds, coverDir+":"+containerCoverDir,
		)
	}
}

// applyPostgresEnv points the app at the Postgres container through the
// host-published port, which keeps the two containers independent of any
// shared docker network.
func applyPostgresEnv(
	env map[string]string,
	postgres testcontainers.Container,
) error {
	if postgres == nil {
		return ctxerrors.New("postgres driver requested without a container")
	}

	env["VIBECHECK_DB_DRIVER"] = DriverPostgres
	env["VIBECHECK_DB_HOSTNAME"] = postgresHostname()
	env["VIBECHECK_DB_PORT"] = postgresPort
	env["VIBECHECK_DB_USERNAME"] = PostgresUser
	env["VIBECHECK_DB_PASSWORD"] = PostgresPassword
	env["VIBECHECK_DB_NAME"] = PostgresDatabase
	env["VIBECHECK_DB_IS_SSL"] = "false"

	return nil
}

// PostgresPort is the host port the Postgres container is published on. A
// test passes it in AppConfig.HostPorts so the app container can reach it.
func PostgresPort(
	ctx context.Context,
	postgres testcontainers.Container,
) (int, error) {
	port, err := postgres.MappedPort(ctx, postgresPort+"/tcp")
	if err != nil {
		return 0, ctxerrors.Wrap(err, "resolve postgres port")
	}

	return int(port.Num()), nil
}

// FreePort reserves an ephemeral port and releases it, so a fake server can
// bind it and the app container can be told about it before it starts.
func FreePort() (int, error) {
	var listenConfig net.ListenConfig

	// #nosec G102 -- Binding all interfaces is the point: the app runs in
	// a container and reaches this port back through the host gateway,
	// which a loopback-only bind would refuse. Test harness, never shipped.
	listener, err := listenConfig.Listen(
		context.Background(), "tcp", "0.0.0.0:0",
	)
	if err != nil {
		return 0, ctxerrors.Wrap(err, "reserve a free port")
	}

	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()

		return 0, ctxerrors.New("listener address was not TCP")
	}

	port := address.Port

	if err := listener.Close(); err != nil {
		return 0, ctxerrors.Wrap(err, "release the reserved port")
	}

	return port, nil
}

// ContainerURLForHostPort is the URL a container uses to reach a server the
// test runs on the host.
func ContainerURLForHostPort(port int) string {
	return "http://" + net.JoinHostPort(hostGateway, strconv.Itoa(port))
}

// HTTPClient is the client the suite talks to the app with.
//
// Redirects are never followed, so a test can assert that an endpoint
// answers directly instead of silently passing on a 301 the client chased.
func HTTPClient() *http.Client {
	return &http.Client{
		Timeout: time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
