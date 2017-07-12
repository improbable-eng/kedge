package winch

import (
	"fmt"
	"reflect"

	"github.com/Bplotka/oidc/login"
	pb "github.com/mwitkow/kedge/_protogen/winch/config"
	"github.com/mwitkow/kedge/lib/auth"
)

var NoAuth auth.Source = nil

type authFactory struct {
	sources map[string]auth.Source
}

func NewAuthFactory() *authFactory {
	return &authFactory{sources: map[string]auth.Source{}}
}

func (f *authFactory) Get(configSource *pb.AuthSource) (auth.Source, error) {
	// Reuse if already constructed.
	if s, ok := f.sources[configSource.Name]; ok {
		return s, nil
	}

	// Lazy construction.
	var source auth.Source
	var err error

	switch s := configSource.GetType().(type) {
	case *pb.AuthSource_Kube:
		source, err = auth.K8s(configSource.Name, s.Kube.Path, s.Kube.User)
	case *pb.AuthSource_Oidc:
		source, err = auth.OIDC(
			configSource.Name,
			login.OIDCConfig{
				Provider:     s.Oidc.Provider,
				ClientID:     s.Oidc.ClientId,
				ClientSecret: s.Oidc.Secret,
				Scopes:       s.Oidc.Scopes,
			},
			s.Oidc.Path,
		)
	case *pb.AuthSource_Dummy:
		source, err = auth.Dummy(
			configSource.Name,
			s.Dummy.Value,
		)
	default:
		return nil, fmt.Errorf("source %v not supported.", reflect.TypeOf(s))
	}

	if err != nil {
		return nil, err
	}
	f.sources[configSource.Name] = source
	return source, nil
}
