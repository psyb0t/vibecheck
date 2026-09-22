package klhayyent

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"

	"github.com/psyb0t/aichteeteapee"
	"github.com/psyb0t/ctxerrors"
)

type requestConfig struct {
	headers http.Header
	query   url.Values
}

// RequestOption changes one request without mutating the client defaults.
type RequestOption func(*requestConfig) error

// WithHeader sets a request header, replacing any default value for that name.
func WithHeader(name, value string) RequestOption {
	return func(config *requestConfig) error {
		if err := validateHeader(name, value); err != nil {
			return err
		}

		config.headers.Set(name, value)

		return nil
	}
}

// WithHeaders sets request headers, replacing defaults with matching names.
func WithHeaders(headers http.Header) RequestOption {
	return func(config *requestConfig) error {
		for name, values := range headers {
			for _, value := range values {
				if err := validateHeader(name, value); err != nil {
					return err
				}
			}

			config.headers.Del(name)

			for _, value := range values {
				config.headers.Add(name, value)
			}
		}

		return nil
	}
}

// WithBearerToken sets a bearer token without exposing it in logs or URLs.
func WithBearerToken(token string) RequestOption {
	return WithHeader(
		aichteeteapee.HeaderNameAuthorization,
		aichteeteapee.AuthSchemeBearer+token,
	)
}

// WithBasicAuth sets an RFC 7617 Basic authorization header.
func WithBasicAuth(username, password string) RequestOption {
	credentials := base64.StdEncoding.EncodeToString(
		[]byte(username + ":" + password),
	)

	return WithHeader(
		aichteeteapee.HeaderNameAuthorization,
		aichteeteapee.AuthSchemeBasic+credentials,
	)
}

// WithQuery sets one query value, replacing any value already set for the key.
func WithQuery(key, value string) RequestOption {
	return func(config *requestConfig) error {
		config.query.Set(key, value)

		return nil
	}
}

// WithQueryValues adds all supplied query values.
func WithQueryValues(values url.Values) RequestOption {
	return func(config *requestConfig) error {
		for key, entries := range values {
			for _, entry := range entries {
				config.query.Add(key, entry)
			}
		}

		return nil
	}
}

func validateHeader(name, value string) error {
	if !isValidHeaderName(name) {
		return ctxerrors.Wrap(ErrInvalidOption, "validate header name")
	}

	if strings.ContainsAny(value, "\r\n") {
		return ctxerrors.Wrap(ErrInvalidOption, "validate header value")
	}

	return nil
}

func isValidHeaderName(name string) bool {
	if name == "" {
		return false
	}

	for index := range len(name) {
		character := name[index]
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			strings.ContainsRune("!#$%&'*+-.^_`|~", rune(character)) {
			continue
		}

		return false
	}

	return true
}
