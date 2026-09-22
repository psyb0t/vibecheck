package klhayyent

import (
	"math"
	"net/http"
	"time"

	"github.com/psyb0t/aichteeteapee"
	"github.com/psyb0t/ctxerrors"
)

const (
	// DefaultMaxResponseBodySize bounds buffered response bodies to 10 MiB.
	DefaultMaxResponseBodySize int64 = 10 << 20
	defaultDependencyName            = "http_client"
	defaultMaxRedirects              = 10
)

type clientConfig struct {
	httpClient          *http.Client
	defaultHeaders      http.Header
	maxResponseBodySize int64
	dependencyName      string
	cookieJar           http.CookieJar
	hasCookieJar        bool
	checkRedirect       func(*http.Request, []*http.Request) error
}

// Option configures a Client at construction time.
type Option func(*clientConfig) error

// WithHTTPClient uses a shallow copy of client for transport settings.
// Later caller changes to client do not change the constructed Client.
func WithHTTPClient(client *http.Client) Option {
	return func(config *clientConfig) error {
		if client == nil {
			return ctxerrors.Wrap(ErrInvalidOption, "set HTTP client")
		}

		clientCopy := *client
		config.httpClient = &clientCopy

		return nil
	}
}

// WithTransport sets the HTTP transport.
func WithTransport(transport http.RoundTripper) Option {
	return func(config *clientConfig) error {
		if transport == nil {
			return ctxerrors.Wrap(ErrInvalidOption, "set HTTP transport")
		}

		config.httpClient.Transport = transport

		return nil
	}
}

// WithTimeout sets the total request timeout. A zero timeout disables it.
func WithTimeout(timeout time.Duration) Option {
	return func(config *clientConfig) error {
		if timeout < 0 {
			return ctxerrors.Wrap(ErrInvalidOption, "set HTTP timeout")
		}

		config.httpClient.Timeout = timeout

		return nil
	}
}

// WithCookieJar installs a caller-owned cookie jar.
func WithCookieJar(cookieJar http.CookieJar) Option {
	return func(config *clientConfig) error {
		if cookieJar == nil {
			return ctxerrors.Wrap(ErrInvalidOption, "set cookie jar")
		}

		config.cookieJar = cookieJar
		config.hasCookieJar = true

		return nil
	}
}

// WithDefaultHeader sets a header sent with every request.
func WithDefaultHeader(name, value string) Option {
	return func(config *clientConfig) error {
		if err := validateHeader(name, value); err != nil {
			return err
		}

		config.defaultHeaders.Set(name, value)

		return nil
	}
}

// WithDefaultHeaders replaces all default headers with a validated copy.
func WithDefaultHeaders(headers http.Header) Option {
	return func(config *clientConfig) error {
		validatedHeaders := make(http.Header, len(headers))
		for name, values := range headers {
			for _, value := range values {
				if err := validateHeader(name, value); err != nil {
					return err
				}

				validatedHeaders.Add(name, value)
			}
		}

		config.defaultHeaders = validatedHeaders

		return nil
	}
}

// WithMaxResponseBodySize sets the largest response body buffered in bytes.
func WithMaxResponseBodySize(size int64) Option {
	return func(config *clientConfig) error {
		if size <= 0 || size == math.MaxInt64 {
			return ctxerrors.Wrap(ErrInvalidOption, "set response body size")
		}

		config.maxResponseBodySize = size

		return nil
	}
}

// WithDependencyName sets the bounded name included in request logs.
func WithDependencyName(name string) Option {
	return func(config *clientConfig) error {
		if name == "" {
			return ctxerrors.Wrap(ErrInvalidOption, "set dependency name")
		}

		config.dependencyName = name

		return nil
	}
}

// WithCheckRedirect adds a redirect policy after the same-origin safety check.
func WithCheckRedirect(
	checkRedirect func(*http.Request, []*http.Request) error,
) Option {
	return func(config *clientConfig) error {
		if checkRedirect == nil {
			return ctxerrors.Wrap(ErrInvalidOption, "set redirect policy")
		}

		config.checkRedirect = checkRedirect

		return nil
	}
}

func defaultClientConfig() clientConfig {
	return clientConfig{
		httpClient: &http.Client{
			Timeout: aichteeteapee.DefaultHTTPClientTimeout,
		},
		defaultHeaders: http.Header{
			aichteeteapee.HeaderNameAccept: []string{
				aichteeteapee.ContentTypeJSON,
			},
		},
		maxResponseBodySize: DefaultMaxResponseBodySize,
		dependencyName:      defaultDependencyName,
	}
}
