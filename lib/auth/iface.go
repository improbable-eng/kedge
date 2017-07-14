package auth

// Source represents a way to add client auth to the request's headers.
type Source interface {
	Name() string
	// HeaderValue allows the source to return value for authorization header.
	HeaderValue() (string, error)
}
