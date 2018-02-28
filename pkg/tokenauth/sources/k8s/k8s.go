package k8sauth

import (
	"context"

	"github.com/Bplotka/oidc/login/k8scache"
	"github.com/improbable-eng/kedge/pkg/tokenauth"
	"github.com/improbable-eng/kedge/pkg/tokenauth/sources/direct"
	"github.com/improbable-eng/kedge/pkg/tokenauth/sources/oauth2"
	"github.com/improbable-eng/kedge/pkg/tokenauth/sources/oidc"
	"github.com/pkg/errors"
	cfg "k8s.io/client-go/tools/clientcmd"
)

// New constructs appropriate tokenAuth Source to the given AuthInfo from kube config referenced by user.
// This is really convenient if you want to reuse well configured kube config.
func New(ctx context.Context, name string, configPath string, userName string) (tokenauth.Source, error) {
	if configPath == "" {
		configPath = k8s.DefaultKubeConfigPath
	}
	k8sConfig, err := cfg.LoadFromFile(configPath)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to load k8s config from file %v. Make sure it is there or change"+
			" permissions.", configPath)
	}

	info, ok := k8sConfig.AuthInfos[userName]
	if !ok {
		return nil, errors.Errorf("Failed to find user %s inside k8s config AuthInfo from file %v", userName, configPath)
	}

	// Currently supported:
	// - token
	// - OIDC
	// - Google compute platform via Oauth2
	if info.AuthProvider != nil {
		switch info.AuthProvider.Name {
		case "oidc":
			cache, err := k8s.NewCacheFromUser(configPath, userName)
			if err != nil {
				return nil, errors.Wrap(err, "Failed to get OIDC configuration from user. ")
			}
			s, _, err := oidcauth.NewWithCache(ctx, name, cache, nil)
			return s, err
		case "gcp":
			c, err := oauth2auth.NewConfigFromMap(info.AuthProvider.Config)
			if err != nil {
				return nil, errors.Wrap(err, "Failed to create OAuth2 config from map.")
			}
			return oauth2auth.NewGCP(name, userName, configPath, c)
		default:
			// TODO(bplotka): Add support for more of them if needed.
			return nil, errors.Errorf("Not supported k8s Auth provider %v", info.AuthProvider.Name)
		}
	}

	if info.Token != "" {
		return directauth.New(name, info.Token), nil
	}

	return nil, errors.Errorf("Not found supported auth source called %s from k8s config %+v", userName, info)
}
