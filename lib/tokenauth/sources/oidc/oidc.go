package oidcauth

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/Bplotka/oidc"
	"github.com/Bplotka/oidc/login"
	"github.com/Bplotka/oidc/login/diskcache"
	"github.com/improbable-eng/kedge/lib/tokenauth"
	"github.com/pkg/errors"
)

type source struct {
	name        string
	cache       login.Cache
	tokenSource oidc.TokenSource
}

// New constructs OIDC tokenauth.Source that optionally supports loging in if callbackSrc is not nil.
// Additionally it returns clearIDToken function that can be used to clear the token if needed.
// TokenSource is by default configured to use disk as cache for tokens.
func New(name string, config login.OIDCConfig, path string, callbackSrv *login.CallbackServer) (tokenauth.Source, func() error, error) {
	return NewWithCache(name, disk.NewCache(path, config), callbackSrv)
}

// NewWithCache is same as New but allows to pass custom cache e.g k8s one.
func NewWithCache(name string, cache login.Cache, callbackSrv *login.CallbackServer) (tokenauth.Source, func() error, error) {
	tokenSource, clearIDTokenFunc, err := login.NewOIDCTokenSource(
		context.Background(),
		log.New(os.Stdout, fmt.Sprintf("OIDC Auth %s ", name), 0),
		login.Config{
			NonceCheck: false,
		},
		cache,
		callbackSrv,
	)
	if err != nil {
		return nil, nil, err
	}

	return &source{
		name:        name,
		cache:       cache,
		tokenSource: tokenSource,
	}, clearIDTokenFunc, nil
}

// Name of the auth source.
func (s *source) Name() string {
	return s.name
}

// Token returns valid ID token or error.
func (s *source) Token(_ context.Context) (string, error) {
	// TODO(bplotka): Add support for ctx.
	token, err := s.tokenSource.OIDCToken()
	if err != nil {
		return "", errors.Wrap(err, "Failed to obtain OIDC Token.")
	}

	return token.IDToken, nil
}
