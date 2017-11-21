package oauth2auth

import (
	"context"

	"github.com/improbable-eng/kedge/lib/tokenauth"
	"github.com/pkg/errors"
	"golang.org/x/oauth2"
)

type source struct {
	name        string
	tokenSource oauth2.TokenSource
}

// New constructs Oauth2 tokenauth that returns valid access token. It is up to token source to do login or not.
func New(name string, tokenSource oauth2.TokenSource) tokenauth.Source {
	return &source{
		name:        name,
		tokenSource: tokenSource,
	}
}

// Name of the auth source.
func (s *source) Name() string {
	return s.name
}

// Token returns valid ID token or error.
func (s *source) Token(_ context.Context) (string, error) {
	token, err := s.tokenSource.Token()
	if err != nil {
		return "", errors.Wrap(err, "Failed to obtain Oauth2 Token.")
	}

	return token.AccessToken, nil
}
