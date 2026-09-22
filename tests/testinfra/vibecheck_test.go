package testinfra

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The coverage plumbing went dead once: the helpers existed, StartApp never
// called them, and the coverage gate silently reported every service package
// as untested instead of failing. These tests assert the request StartApp
// actually builds, so the wiring cannot rot back out without a red test.
//
// The tests that call t.Setenv stay serial, because the environment is
// process-global state.

func TestAppRequestUsesThePlainImageWhenCoverageIsOff(t *testing.T) {
	t.Parallel()

	req := appRequest(AppConfig{}, map[string]string{}, "/repo", "")

	assert.Equal(
		t, appImageTag, req.Tag,
		"an ordinary run must use the uninstrumented production image",
	)
	assert.Nil(
		t, req.BuildArgs,
		"an ordinary build passes no coverage flags",
	)
	assert.Nil(
		t, req.HostConfigModifier,
		"no covdata mount belongs on an ordinary run",
	)
}

func TestAppRequestInstrumentsTheImageWhenCoverageIsOn(t *testing.T) {
	t.Parallel()

	const coverDir = "/workspace/.cover/covdata"

	req := appRequest(AppConfig{}, map[string]string{}, "/repo", coverDir)

	assert.Equal(
		t, appImageTagCover, req.Tag,
		"an instrumented build gets its own tag",
	)
	assert.NotEqual(
		t, appImageTag, req.Tag,
		"an instrumented image must never reuse the production tag",
	)

	args := req.BuildArgs
	require.Contains(t, args, "BUILD_COVER")
	require.NotNil(t, args["BUILD_COVER"])

	cover := *args["BUILD_COVER"]
	assert.Contains(t, cover, "-cover")
	assert.Contains(
		t, cover, "-coverpkg="+coveredModule+"/...",
		"the whole module is instrumented, not just the main package",
	)
}

func TestAppRequestMountsTheCovdataDirectory(t *testing.T) {
	t.Parallel()

	const coverDir = "/workspace/.cover/covdata"

	req := appRequest(AppConfig{}, map[string]string{}, "/repo", coverDir)

	require.NotNil(
		t, req.HostConfigModifier,
		"a coverage run has to bind mount the covdata directory",
	)

	hostConfig := container.HostConfig{}
	req.HostConfigModifier(&hostConfig)

	require.Len(t, hostConfig.Binds, 1)
	assert.Equal(
		t, coverDir+":"+containerCoverDir, hostConfig.Binds[0],
		"the host path is bind mounted at the container's covdata path",
	)

	// testcontainers validates a bind as host:container, optionally with a
	// mode. A malformed entry is rejected before the container starts.
	assert.Len(t, strings.Split(hostConfig.Binds[0], ":"), 2)
}

// mountCoverDir must append rather than assign, because testcontainers wraps
// the modifier for host port forwarding and may have put binds there first.
func TestMountCoverDirPreservesExistingBinds(t *testing.T) {
	t.Parallel()

	hostConfig := container.HostConfig{Binds: []string{"/a:/b"}}

	mountCoverDir("/cover")(&hostConfig)

	assert.Equal(t, []string{"/a:/b", "/cover:" + containerCoverDir},
		hostConfig.Binds)
}

// A directory the container cannot write must fail loudly. Continuing would
// produce an empty profile and report every containerised package as dead
// code, which is exactly the silent failure this plumbing exists to prevent.
func TestCoverDirVerdict(t *testing.T) {
	t.Parallel()

	chmodErr := errors.New("operation not permitted")

	testCases := []struct {
		name     string
		perm     os.FileMode
		wantErr  bool
		contains string
	}{
		{
			name:    "world writable survives a failed chmod",
			perm:    0o777,
			wantErr: false,
		},
		{
			name:     "group writable only is refused",
			perm:     0o775,
			wantErr:  true,
			contains: "silently be empty",
		},
		{
			name:     "owner writable only is refused",
			perm:     0o755,
			wantErr:  true,
			contains: "0755",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := coverDirVerdict("/covdata", tc.perm, chmodErr)

			if !tc.wantErr {
				assert.NoError(
					t, err,
					"the container can still write, so the run continues",
				)

				return
			}

			require.Error(t, err)
			assert.Contains(
				t, err.Error(), tc.contains,
				"the refusal names what an operator has to fix",
			)
		})
	}
}

func TestPrepareCoverDirIsOffWhenTheEnvVarIsUnset(t *testing.T) {
	t.Setenv(coverDirEnvVar, "")

	dir, err := prepareCoverDir(t.Context())
	require.NoError(t, err)
	assert.Empty(t, dir, "coverage stays off unless the gate asks for it")
}

func TestPrepareCoverDirCreatesAWritableDirectory(t *testing.T) {
	target := t.TempDir() + "/covdata"
	t.Setenv(coverDirEnvVar, target)

	dir, err := prepareCoverDir(t.Context())
	require.NoError(t, err)
	assert.Equal(t, target, dir)

	info, err := os.Stat(target)
	require.NoError(t, err, "the covdata directory is created")
	assert.True(t, info.IsDir())

	// The container writes as a different UID than the test process, so the
	// directory has to be writable by everyone or the counters are lost and
	// coverage silently reads as zero.
	assert.Equal(
		t, os.FileMode(coverModePerm), info.Mode().Perm(),
		"the container's non-root user must be able to write here",
	)
}

// A covdata directory left behind by a different UID must not take the whole
// suite down. It only matters that the container can write into it.
func TestPrepareCoverDirToleratesAForeignButWritableDirectory(t *testing.T) {
	target := t.TempDir() + "/covdata"
	require.NoError(t, os.MkdirAll(target, coverModePerm))
	require.NoError(t, os.Chmod(target, coverModePerm))

	t.Setenv(coverDirEnvVar, target)

	dir, err := prepareCoverDir(t.Context())
	require.NoError(t, err)
	assert.Equal(t, target, dir)
}

// The app has to be told where to write counters, or an instrumented binary
// drops them on exit and the gate sees nothing.
func TestAppEnvSetsGocoverdirOnlyForACoverageRun(t *testing.T) {
	base := AppConfig{ProviderBaseURL: "http://example.invalid"}

	t.Run("off", func(t *testing.T) {
		t.Setenv(coverDirEnvVar, "")

		env, err := appEnv(t.Context(), base, nil)
		require.NoError(t, err)
		assert.NotContains(t, env, "GOCOVERDIR")
	})

	t.Run("on", func(t *testing.T) {
		t.Setenv(coverDirEnvVar, t.TempDir())

		env, err := appEnv(t.Context(), base, nil)
		require.NoError(t, err)
		assert.Equal(t, containerCoverDir, env["GOCOVERDIR"])
	})
}
