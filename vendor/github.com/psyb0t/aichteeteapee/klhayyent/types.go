package klhayyent

import (
	"fmt"
	"net/http"

	"github.com/psyb0t/aichteeteapee"
)

// Response is the bounded response returned for every completed HTTP exchange.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// HTTPError describes a non-2xx response and unwraps to its HTTP status error.
type HTTPError struct {
	Response      *Response
	ErrorResponse aichteeteapee.ErrorResponse
	cause         error
}

func (e *HTTPError) Error() string {
	if e == nil || e.Response == nil {
		return "HTTP request failed"
	}

	return fmt.Sprintf(
		"HTTP request failed with status %d",
		e.Response.StatusCode,
	)
}

func (e *HTTPError) Unwrap() error {
	if e == nil {
		return nil
	}

	return e.cause
}
