package klhayyent

import "errors"

var (
	ErrInvalidBaseURL       = errors.New("invalid base URL")
	ErrInvalidRequestTarget = errors.New("invalid request target")
	ErrInvalidOption        = errors.New("invalid client option")
	ErrResponseBodyTooLarge = errors.New("response body too large")
)
