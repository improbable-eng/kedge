package auth

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/Bplotka/oidc"
	"github.com/Bplotka/oidc/login"
	"github.com/Bplotka/oidc/login/diskcache"
)

type oidcSource struct {
	name        string
	tokenSource oidc.TokenSource
}

func OIDC(name string, config login.OIDCConfig, path string, callbackSrv *login.CallbackServer) (Source, error) {
	return oidcWithCache(name, disk.NewCache(path, config), callbackSrv)
}

func oidcWithCache(name string, cache login.Cache, callbackSrv *login.CallbackServer) (Source, error) {
	tokenSource, err := login.NewOIDCTokenSource(
		context.Background(),
		log.New(os.Stdout, "oidc auth", 0),
		login.Config{
			NonceCheck: false,
		},
		cache,
		callbackSrv,
	)
	if err != nil {
		return nil, err
	}

	return &oidcSource{
		name:        name,
		tokenSource: tokenSource,
	}, nil
}

func (s *oidcSource) Name() string {
	return s.name
}

func (s *oidcSource) HeaderValue() (string, error) {
	token, err := s.tokenSource.OIDCToken()
	if err != nil {
		return "", fmt.Errorf("failed to obtain ID Token. Err: %v", err)
	}

	return fmt.Sprintf("Bearer %s", token.IDToken), nil
}
