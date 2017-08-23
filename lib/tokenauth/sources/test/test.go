package testauth

import "context"

// Source is testing tokenauth.Source that can be mocked.
type Source struct {
	NameValue  string
	TokenValue string
	Err        error
}

// Name of the auth Source.
func (s *Source) Name() string {
	return s.NameValue
}

// Token returns a token given while constructing.
func (s *Source) Token(_ context.Context) (string, error) {
	return s.TokenValue, s.Err
}
