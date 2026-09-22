// Package secrets holds the one cryptographic operation Vibecheck performs:
// encrypting retained evaluation input.
//
// Retention is off by default. When an operator turns it on, the raw request
// is stored as AEAD ciphertext and nothing else in the row reveals it. There
// is deliberately no way to enable retention without supplying a key, because
// "store the inputs" and "store them in the clear" must never be the same
// setting.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"

	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxerrors/commerr"
)

// DataKeyBytes is the required decoded length of the configured data key.
// AES-256-GCM, so 32 bytes.
const DataKeyBytes = 32

// Sealer encrypts and decrypts retained input.
//
// The zero value is not usable. A nil *Sealer means retention is disabled,
// and every method on it reports that rather than panicking, so callers do
// not have to branch on configuration at each call site.
type Sealer struct {
	aead cipher.AEAD
}

// NewSealer builds a sealer from a base64 data key.
//
// The key must decode to exactly DataKeyBytes. A shorter key is refused
// rather than stretched, because silently accepting a weak key is how
// retention ends up looking encrypted without being so.
func NewSealer(base64Key string) (*Sealer, error) {
	if base64Key == "" {
		return nil, ctxerrors.Wrap(
			commerr.ErrRequiredConfigValueNotSet,
			"input retention is enabled but no data key is set",
		)
	}

	key, err := base64.StdEncoding.DecodeString(base64Key)
	if err != nil {
		return nil, ctxerrors.Wrap(
			commerr.ErrInvalidValue, "data key is not valid base64",
		)
	}

	if len(key) != DataKeyBytes {
		return nil, ctxerrors.Wrapf(
			commerr.ErrInvalidValue,
			"data key must decode to %d bytes, got %d", DataKeyBytes, len(key),
		)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "build cipher from data key")
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "build AEAD from cipher")
	}

	return &Sealer{aead: aead}, nil
}

// Enabled reports whether this sealer can actually encrypt.
func (s *Sealer) Enabled() bool {
	return s != nil && s.aead != nil
}

// Seal encrypts plaintext and returns nonce||ciphertext.
//
// A nil sealer returns nil with no error: retention is off, so there is
// nothing to store and that is not a failure.
func (s *Sealer) Seal(plaintext []byte) ([]byte, error) {
	if !s.Enabled() {
		return nil, nil
	}

	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, ctxerrors.Wrap(err, "draw nonce for retained input")
	}

	return s.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Open decrypts a value produced by Seal.
func (s *Sealer) Open(sealed []byte) ([]byte, error) {
	if !s.Enabled() {
		return nil, ctxerrors.Wrap(
			commerr.ErrUnavailable, "input retention is not enabled",
		)
	}

	nonceSize := s.aead.NonceSize()
	if len(sealed) < nonceSize {
		return nil, ctxerrors.Wrap(
			commerr.ErrInvalidValue, "retained input is shorter than its nonce",
		)
	}

	plaintext, err := s.aead.Open(
		nil, sealed[:nonceSize], sealed[nonceSize:], nil,
	)
	if err != nil {
		return nil, ctxerrors.Wrap(
			commerr.ErrParseFailed, "retained input failed authentication",
		)
	}

	return plaintext, nil
}

// GenerateDataKey returns a fresh base64 data key. It exists so an operator
// can mint one with the shipped binary rather than reaching for openssl and
// guessing the length.
func GenerateDataKey() (string, error) {
	key := make([]byte, DataKeyBytes)
	if _, err := rand.Read(key); err != nil {
		return "", ctxerrors.Wrap(err, "draw data key")
	}

	return base64.StdEncoding.EncodeToString(key), nil
}
