package secrets_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/psyb0t/ctxerrors/commerr"
	"github.com/psyb0t/vibecheck/internal/pkg/secrets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSealRoundTrip(t *testing.T) {
	t.Parallel()

	key, err := secrets.GenerateDataKey()
	require.NoError(t, err)

	sealer, err := secrets.NewSealer(key)
	require.NoError(t, err)
	require.True(t, sealer.Enabled())

	plaintext := []byte(`{"state":"synthetic","facts":{"a":true}}`)

	sealed, err := sealer.Seal(plaintext)
	require.NoError(t, err)
	assert.NotContains(t, string(sealed), "synthetic",
		"the retained value must not be readable without the key")

	opened, err := sealer.Open(sealed)
	require.NoError(t, err)
	assert.Equal(t, plaintext, opened)
}

func TestSealProducesADistinctCiphertextEachTime(t *testing.T) {
	t.Parallel()

	sealer := newSealer(t)
	plaintext := []byte("the same input twice")

	first, err := sealer.Seal(plaintext)
	require.NoError(t, err)

	second, err := sealer.Seal(plaintext)
	require.NoError(t, err)

	assert.NotEqual(t, first, second,
		"a fresh nonce per seal is what stops two identical inputs looking identical at rest") //nolint:lll // one string literal; splitting it would change the text
}

func TestDisabledSealerIsANoOp(t *testing.T) {
	t.Parallel()

	var sealer *secrets.Sealer

	assert.False(t, sealer.Enabled())

	sealed, err := sealer.Seal([]byte("anything"))
	require.NoError(t, err)
	assert.Nil(t, sealed, "retention off means nothing is stored, not an error")

	_, err = sealer.Open([]byte("anything"))
	require.ErrorIs(t, err, commerr.ErrUnavailable)
}

func TestNewSealerRejectsBadKeys(t *testing.T) {
	t.Parallel()

	shortKey := base64.StdEncoding.EncodeToString(
		[]byte(strings.Repeat("k", secrets.DataKeyBytes-1)),
	)
	longKey := base64.StdEncoding.EncodeToString(
		[]byte(strings.Repeat("k", secrets.DataKeyBytes+1)),
	)

	testCases := []struct {
		name    string
		key     string
		wantErr error
	}{
		{
			name:    "empty key",
			key:     "",
			wantErr: commerr.ErrRequiredConfigValueNotSet,
		},
		{
			name:    "not base64",
			key:     "!!! not base64 !!!",
			wantErr: commerr.ErrInvalidValue,
		},
		{
			name:    "one byte short",
			key:     shortKey,
			wantErr: commerr.ErrInvalidValue,
		},
		{
			name:    "one byte long",
			key:     longKey,
			wantErr: commerr.ErrInvalidValue,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := secrets.NewSealer(tc.key)
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestOpenRejectsTamperedCiphertext(t *testing.T) {
	t.Parallel()

	sealer := newSealer(t)

	sealed, err := sealer.Seal([]byte("retained input"))
	require.NoError(t, err)

	tampered := append([]byte(nil), sealed...)
	tampered[len(tampered)-1] ^= 0xFF

	_, err = sealer.Open(tampered)
	require.ErrorIs(t, err, commerr.ErrParseFailed,
		"AEAD authentication is the point: a flipped bit must not decrypt")
}

func TestOpenRejectsATruncatedValue(t *testing.T) {
	t.Parallel()

	sealer := newSealer(t)

	_, err := sealer.Open([]byte{0x01, 0x02})
	require.ErrorIs(t, err, commerr.ErrInvalidValue)
}

func TestOpenRejectsAnotherKeysCiphertext(t *testing.T) {
	t.Parallel()

	sealed, err := newSealer(t).Seal([]byte("retained input"))
	require.NoError(t, err)

	_, err = newSealer(t).Open(sealed)
	require.ErrorIs(t, err, commerr.ErrParseFailed)
}

func TestGenerateDataKeyIsAcceptedByNewSealer(t *testing.T) {
	t.Parallel()

	key, err := secrets.GenerateDataKey()
	require.NoError(t, err)

	decoded, err := base64.StdEncoding.DecodeString(key)
	require.NoError(t, err)
	assert.Len(t, decoded, secrets.DataKeyBytes)

	_, err = secrets.NewSealer(key)
	require.NoError(t, err)
}

func newSealer(t *testing.T) *secrets.Sealer {
	t.Helper()

	key, err := secrets.GenerateDataKey()
	require.NoError(t, err)

	sealer, err := secrets.NewSealer(key)
	require.NoError(t, err)

	return sealer
}
