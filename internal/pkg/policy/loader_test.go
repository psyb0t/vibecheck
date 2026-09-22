package policy_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/psyb0t/vibecheck/internal/pkg/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// minimalYAML is the smallest document that compiles. Tests that need a second
// distinct policy vary its name or version.
func minimalYAML(name, version string) string {
	return `apiVersion: ` + policy.APIVersion + `
kind: ` + policy.Kind + `
metadata:
  name: ` + name + `
  version: "` + version + `"
spec:
  outcomes: [allow, review]
  defaultOutcome: review
  questions:
    risky:
      type: noul
      instructions: Is this risky?
`
}

func TestParseDocumentAcceptsYAMLAndJSON(t *testing.T) {
	t.Parallel()

	jsonDoc := `{
  "apiVersion": "` + policy.APIVersion + `",
  "kind": "` + policy.Kind + `",
  "metadata": {"name": "json-policy", "version": "2.0.0"},
  "spec": {
    "outcomes": ["allow", "review"],
    "defaultOutcome": "review",
    "questions": {"risky": {"type": "noul", "instructions": "Is this risky?"}}
  }
}`

	testCases := []struct {
		name        string
		data        string
		wantName    string
		wantVersion string
	}{
		{
			"yaml",
			minimalYAML("yaml-policy", testPolicyVersion),
			"yaml-policy",
			testPolicyVersion,
		},
		{"json", jsonDoc, "json-policy", "2.0.0"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			compiled, err := policy.CompileBytes([]byte(tc.data), 0)
			require.NoError(t, err)
			assert.Equal(
				t,
				policy.Ref{Name: tc.wantName, Version: tc.wantVersion},
				compiled.Ref,
				"one parser and one compiler serve both mounted files and inline policies", //nolint:lll // one string literal; splitting it would change the text
			)
		})
	}
}

func TestParseDocumentRejectsMalformedInput(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		data string
	}{
		{"empty", ""},
		{"whitespace only", "   \n\t\n"},
		{"not yaml at all", "\x00\x01\x02 not a document"},
		{
			"unknown field",
			minimalYAML("typo-policy", testPolicyVersion) +
				"  unexpectedKey: true\n",
		},
		{
			"two documents",
			minimalYAML("first", testPolicyVersion) + "---\n" +
				minimalYAML("second", testPolicyVersion),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := policy.ParseDocument([]byte(tc.data), 0)
			require.ErrorIs(t, err, policy.ErrInvalidDocument)
		})
	}
}

func TestParseDocumentEnforcesTheSizeCeiling(t *testing.T) {
	t.Parallel()

	atCeiling := minimalYAML("size-policy", testPolicyVersion) +
		"  # " + strings.Repeat("p", 60) + "\n"
	limit := len(atCeiling)

	t.Run("at the ceiling", func(t *testing.T) {
		t.Parallel()

		_, err := policy.ParseDocument([]byte(atCeiling), limit)
		require.NoError(t, err)
	})

	t.Run("one byte over the ceiling", func(t *testing.T) {
		t.Parallel()

		oversized := atCeiling + "p"
		require.Len(t, oversized, limit+1)

		_, err := policy.ParseDocument([]byte(oversized), limit)
		require.ErrorIs(t, err, policy.ErrPolicyTooLarge)
	})
}

func TestLoadDirCompilesEveryPolicyFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", minimalYAML(policyNameAlpha, testPolicyVersion))
	writeFile(t, dir, "b.yml", minimalYAML("beta", testPolicyVersion))
	writeFile(t, dir, "notes.md", "# not a policy")
	writeFile(t, dir, "scratch.txt", "also not a policy")

	set, err := policy.LoadDir(dir, 0)
	require.NoError(t, err)

	assert.Equal(t, 2, set.Len(), "only .yaml and .yml files are policies")
	assert.Equal(
		t,
		[]policy.Ref{
			{Name: policyNameAlpha, Version: testPolicyVersion},
			{Name: "beta", Version: testPolicyVersion},
		},
		set.Refs(),
		"refs are ordered by name then version",
	)

	alpha, ok := set.Get(policy.Ref{
		Name: policyNameAlpha, Version: testPolicyVersion,
	})
	require.True(t, ok)
	assert.Equal(t, policyNameAlpha, alpha.Ref.Name)

	_, ok = set.Get(policy.Ref{Name: policyNameAlpha, Version: "9.9.9"})
	assert.False(t, ok, "a policy ref is name plus version, not name alone")
}

func TestLoadDirOnEmptyInput(t *testing.T) {
	t.Parallel()

	t.Run("empty directory", func(t *testing.T) {
		t.Parallel()

		set, err := policy.LoadDir(t.TempDir(), 0)
		require.NoError(t, err)
		assert.Equal(t, 0, set.Len())
	})

	t.Run("no directory configured", func(t *testing.T) {
		t.Parallel()

		set, err := policy.LoadDir("", 0)
		require.NoError(t, err)
		assert.Equal(
			t,
			0,
			set.Len(),
			"inline-only deployments start with no files",
		)
	})

	t.Run("missing directory", func(t *testing.T) {
		t.Parallel()

		_, err := policy.LoadDir(filepath.Join(t.TempDir(), "nope"), 0)
		require.Error(
			t,
			err,
			"a configured but absent policy directory is a startup failure",
		)
	})
}

func TestLoadDirRejectsDuplicateIdentity(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, dir, "one.yaml", minimalYAML("same", testPolicyVersion))
	writeFile(t, dir, "two.yaml", minimalYAML("same", testPolicyVersion))

	_, err := policy.LoadDir(dir, 0)
	require.ErrorIs(t, err, policy.ErrDuplicatePolicy)
}

func TestLoadDirAcceptsTheSameNameAtDifferentVersions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, dir, "v1.yaml", minimalYAML("same", testPolicyVersion))
	writeFile(t, dir, "v2.yaml", minimalYAML("same", "2.0.0"))

	set, err := policy.LoadDir(dir, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, set.Len())
}

func TestLoadDirRefusesSymlinks(t *testing.T) {
	t.Parallel()

	outside := t.TempDir()
	target := filepath.Join(outside, "real.yaml")
	require.NoError(
		t,
		os.WriteFile(
			target,
			[]byte(minimalYAML("linked", testPolicyVersion)),
			0o600,
		),
	)

	dir := t.TempDir()
	require.NoError(t, os.Symlink(target, filepath.Join(dir, "link.yaml")))

	_, err := policy.LoadDir(dir, 0)
	require.ErrorIs(
		t, err, policy.ErrUnsafePolicyPath,
		"a symlink is refused even when its target is a valid policy",
	)
}

func TestLoadDirRefusesNonRegularFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "nested.yaml"), 0o750))

	_, err := policy.LoadDir(dir, 0)
	require.ErrorIs(t, err, policy.ErrUnsafePolicyPath)
}

func TestLoadDirEnforcesTheSizeCeiling(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	body := minimalYAML("big", testPolicyVersion)
	writeFile(t, dir, "big.yaml", body)

	_, err := policy.LoadDir(dir, len(body)-1)
	require.ErrorIs(t, err, policy.ErrPolicyTooLarge)
}

func TestLoadDirPropagatesCompileFailures(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, dir, "broken.yaml", strings.Replace(
		minimalYAML("broken", testPolicyVersion),
		"defaultOutcome: review",
		"defaultOutcome: escalate",
		1,
	))

	_, err := policy.LoadDir(dir, 0)
	require.ErrorIs(
		t, err, policy.ErrInvalidOutcome,
		"one bad file fails startup rather than loading a partial set",
	)
}

func TestNewSetIsOrderedAndComplete(t *testing.T) {
	t.Parallel()

	first, err := policy.CompileBytes(
		[]byte(minimalYAML("zeta", testPolicyVersion)),
		0,
	)
	require.NoError(t, err)

	second, err := policy.CompileBytes(
		[]byte(minimalYAML(policyNameAlpha, "2.0.0")),
		0,
	)
	require.NoError(t, err)

	set, err := policy.NewSet([]*policy.Compiled{first, second})
	require.NoError(t, err)

	all := set.All()
	require.Len(t, all, 2)
	assert.Equal(t, policyNameAlpha, all[0].Ref.Name)
	assert.Equal(t, "zeta", all[1].Ref.Name)
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()

	require.NoError(
		t,
		os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600),
	)
}

func TestLoadDirWithMaxQuestionsEnforcesDeploymentCeiling(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	body := minimalYAML(policyNameAlpha, testPolicyVersion) + `    second:
      type: noul
      instructions: Is this second question risky?
`
	writeFile(t, dir, "policy.yaml", body)

	_, err := policy.LoadDirWithMaxQuestions(dir, 0, 1)
	require.ErrorIs(t, err, policy.ErrInvalidQuestion)
}
