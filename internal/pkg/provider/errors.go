package provider

import "errors"

// Provider failure classes. Callers map these to their own stable error codes
// and never surface the provider's own response body, which can echo request
// content back.
//
// Most classes reuse a commerr sentinel at the call site rather than being
// redeclared here. Only the two cases commerr has no equivalent for are
// declared locally.
var (
	// ErrResponseTooLarge means the provider's response exceeded the
	// configured ceiling and was refused before being decoded.
	ErrResponseTooLarge = errors.New("provider: response exceeds the configured size limit") //nolint:lll // one sentinel message, splitting it would change the text

	// ErrOverloaded means the provider reported that it is temporarily out
	// of capacity. It is separate from a rate limit: the caller did nothing
	// wrong and the same request may succeed shortly.
	ErrOverloaded = errors.New("provider: temporarily overloaded")
)
