package auth

import (
	"fmt"

	"github.com/Bplotka/oidc/login/k8scache"
	cfg "k8s.io/client-go/tools/clientcmd"
)

// K8s is a special source that get's auth info from kubeconfig and returns deducted source.
func K8s(name string, configPath string, userName string) (Source, error) {
	if configPath == "" {
		configPath = k8s.DefaultKubeConfigPath
	}
	k8sConfig, err := cfg.LoadFromFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("Failed to load k8s config from file %v. Make sure it is there or change"+
			" permissions. Err: %v", configPath, err)
	}

	info, ok := k8sConfig.AuthInfos[userName]
	if !ok {
		return nil, fmt.Errorf("Failed to find user %s inside k8s config AuthInfo from file %v", userName, configPath)
	}

	if info.AuthProvider != nil {
		switch info.AuthProvider.Name {
		case "oidc":
			cache, err := k8s.NewCacheFromUser(configPath, userName)
			if err != nil {
				return nil, fmt.Errorf("Failed to get OIDC configuration from user. Err: %v", err)
			}
			return oidcWithCache(name, cache, nil)
		default:
			// TODO(bplotka): Add support for more of them.
			return nil, fmt.Errorf("Not supported k8s Auth provider %v", info.AuthProvider.Name)
		}
	}

	return nil, fmt.Errorf("Not found supported auth source from k8s config %+v", info)
}
