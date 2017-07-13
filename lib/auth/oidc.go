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

func OIDC(name string, config login.OIDCConfig, path string) (Source, error) {
	return oidcWithCache(name, disk.NewCache(path, config))
}

func oidcWithCache(name string, cache login.Cache) (Source, error) {
	tokenSource, err := login.NewOIDCTokenSource(
		context.Background(),
		log.New(os.Stdout, "oidc auth", 0),
		login.Config{
			DisableLogin: true,
			NonceCheck:   false,
		},
		cache,
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
