package winch

import (
	"fmt"
	"net/http"
	"reflect"

	"github.com/Bplotka/oidc/login"
	"github.com/Bplotka/oidc/login/diskcache"
	pb "github.com/mwitkow/kedge/_protogen/winch/config"
	"github.com/mwitkow/kedge/lib/tokenauth"
	"github.com/mwitkow/kedge/lib/tokenauth/sources/direct"
	"github.com/mwitkow/kedge/lib/tokenauth/sources/k8s"
	"github.com/mwitkow/kedge/lib/tokenauth/sources/oidc"
)

var NoAuth tokenauth.Source = nil

type AuthFactory struct {
	listenAddress string
	mux           *http.ServeMux
	sources       map[string]tokenauth.Source
}

func NewAuthFactory(listenAddress string, mux *http.ServeMux) *AuthFactory {
	return &AuthFactory{
		sources:       map[string]tokenauth.Source{},
		listenAddress: listenAddress,
		mux:           mux,
	}
}

func (f *AuthFactory) Get(configSource *pb.AuthSource) (tokenauth.Source, error) {
	// Reuse if already constructed.
	if s, ok := f.sources[configSource.Name]; ok {
		return s, nil
	}

	// Lazy construction.
	var source tokenauth.Source
	var err error

	switch s := configSource.GetType().(type) {
	case *pb.AuthSource_Kube:
		source, err = k8sauth.New(configSource.Name, s.Kube.Path, s.Kube.User)
	case *pb.AuthSource_Oidc:
		var callbackSrv *login.CallbackServer
		if s.Oidc.LoginCallbackPath != "" {
			callbackSrv = login.NewReuseServer(s.Oidc.LoginCallbackPath, f.listenAddress, f.mux)
		}

		cache := disk.NewCache(
			s.Oidc.Path,
			login.OIDCConfig{
				Provider:     s.Oidc.Provider,
				ClientID:     s.Oidc.ClientId,
				ClientSecret: s.Oidc.Secret,
				Scopes:       s.Oidc.Scopes,
			},
		)

		var clearIDTokenFunc func() error
		source, clearIDTokenFunc, err = oidcauth.NewWithCache(
			configSource.Name,
			cache,
			callbackSrv,
		)
		// Register handler for clearing ID token.
		f.mux.HandleFunc(fmt.Sprintf("/winch/cleartoken/%s", configSource.Name), oidcClearTokenHandler(clearIDTokenFunc))

	case *pb.AuthSource_Dummy:
		source = directauth.New(
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

func oidcClearTokenHandler(clearIDTokenFunc func() error) http.HandlerFunc {
	return func(resp http.ResponseWriter, req *http.Request) {
		err := clearIDTokenFunc()
		resp.Header().Set("content-type", "text/plain")
		if err != nil {
			resp.Header().Set("x-winch-err", err.Error())
			resp.Write([]byte(fmt.Sprintf("Error: %s", err.Error())))
			resp.WriteHeader(http.StatusInternalServerError)
			return
		}
		resp.Write([]byte("Token cleared"))
		resp.WriteHeader(http.StatusOK)
		return
	}
}
