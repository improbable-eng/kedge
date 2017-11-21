package tokenauth

import "context"

// Source represents a way to get client auth in a form of token.
type Source interface {
	// Name of the auth source.
	Name() string

	// Token allows the source to return a valid token for specific authorization type in a form of string.
	//
	// Example usage:
	// - filling Authorization HTTP header with valid auth.
	// In that case it is up to caller to properly save it into specific http request header (usually called "Authorization")
	// and add "bearer" prefix if needed.
	Token(context.Context) (string, error)
}
