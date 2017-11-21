package directauth

import (
	"context"

	"github.com/improbable-eng/kedge/pkg/tokenauth"
)

type source struct {
	name  string
	token string
}

// New returns new direct auth source.
func New(name string, token string) tokenauth.Source {
	return &source{
		name:  name,
		token: token,
	}
}

// Name of the auth source.
func (s *source) Name() string {
	return s.name
}

// Token returns a token given in constructor.
func (s *source) Token(_ context.Context) (string, error) {
	return s.token, nil
}
