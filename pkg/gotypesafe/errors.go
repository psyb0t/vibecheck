package typesafe

import "errors"

var (
	// ErrResponseTooLarge means the response exceeded the configured ceiling.
	ErrResponseTooLarge = errors.New(
		"typesafe: response exceeds configured size limit",
	)

	// ErrOverloaded means TypeSafe reported that it has no spare capacity.
	ErrOverloaded = errors.New("typesafe: provider temporarily overloaded")
)
