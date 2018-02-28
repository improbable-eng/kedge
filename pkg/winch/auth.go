package winch

import (
	"fmt"
	"net/http"
	"reflect"

	"io/ioutil"

	"context"
	"time"

	"github.com/Bplotka/oidc/login"
	"github.com/Bplotka/oidc/login/diskcache"
	"github.com/improbable-eng/kedge/pkg/tokenauth"
	"github.com/improbable-eng/kedge/pkg/tokenauth/sources/direct"
	"github.com/improbable-eng/kedge/pkg/tokenauth/sources/k8s"
	"github.com/improbable-eng/kedge/pkg/tokenauth/sources/oidc"
	"github.com/improbable-eng/kedge/pkg/tokenauth/sources/test"
	pb "github.com/improbable-eng/kedge/protogen/winch/config"
	"github.com/pkg/errors"
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

	// Get is always use on startup only, so we can set reasonable timeout here.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	switch s := configSource.GetType().(type) {
	case *pb.AuthSource_Kube:
		source, err = k8sauth.New(ctx, configSource.Name, s.Kube.Path, s.Kube.User)
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
			ctx,
			configSource.Name,
			cache,
			callbackSrv,
		)
		// Register handler for clearing ID token.
		f.mux.HandleFunc(fmt.Sprintf("/winch/cleartoken/%s", configSource.Name), oidcClearTokenHandler(clearIDTokenFunc))
	case *pb.AuthSource_ServiceAccountOidc:
		serviceAccountJson, err := ioutil.ReadFile(s.ServiceAccountOidc.ServiceAccountJsonPath)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to open google SA JSON file from %s", s.ServiceAccountOidc.ServiceAccountJsonPath)
		}

		source, err = oidcauth.NewGoogleFromServiceAccount(
			ctx,
			configSource.Name,
			login.OIDCConfig{
				Provider:     s.ServiceAccountOidc.Provider,
				ClientID:     s.ServiceAccountOidc.ClientId,
				ClientSecret: s.ServiceAccountOidc.Secret,
				Scopes:       s.ServiceAccountOidc.Scopes,
			},
			serviceAccountJson,
		)
	case *pb.AuthSource_Dummy:
		testSource := &testauth.Source{
			NameValue:  configSource.Name,
			TokenValue: s.Dummy.Value,
		}
		if s.Dummy.Value == "" {
			// Let's trigger error on that.
			testSource.Err = errors.New("Error dummy auth source. No TokenValue specified")
		}
		source = testSource
	case *pb.AuthSource_Token:
		source = directauth.New(configSource.Name, s.Token.GetToken())
	default:
		return nil, fmt.Errorf("source %v not supported", reflect.TypeOf(s))
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
