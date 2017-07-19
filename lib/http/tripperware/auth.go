package tripperware

import (
	"net/http"

	"github.com/mwitkow/go-httpwares/tags"
	"github.com/mwitkow/kedge/lib/auth"
	"github.com/mwitkow/kedge/lib/http/ctxtags"
	"github.com/mwitkow/kedge/lib/map"
)

const (
	ProxyAuthHeader = "Proxy-Authorization"
	authHeader      = "Authorization"
)

// authTripper is a piece of tripperware that injects auth defined per route to authorize request for.
// NOTE: It requires to have mappingTripper before itself to put the routing inside context.
type authTripper struct {
	parent http.RoundTripper

	authHeader    string
	authTag       string
	authFromRoute func(route *kedge_map.Route) (auth.Source, bool)
}

func (t *authTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	route, ok, err := getRoute(req.Context())
	if err != nil {
		return nil, err
	}
	if !ok {
		return t.parent.RoundTrip(req)
	}

	authSource, ok := t.authFromRoute(route)
	if authSource == nil {
		// No auth configured.
		return t.parent.RoundTrip(req)
	}

	tags := http_ctxtags.ExtractInbound(req)
	tags.Set(t.authTag, authSource.Name())

	val, err := authSource.HeaderValue()
	if err != nil {
		return nil, err
	}

	req.Header.Set(t.authHeader, val)
	return t.parent.RoundTrip(req)
}

func (t *authTripper) Clone() RoundTripper {
	return &(*t)
}

func WrapForProxyAuth(parentTransport RoundTripper) RoundTripper {
	return &authTripper{
		parent:     parentTransport.Clone(),
		authHeader: ProxyAuthHeader,
		authTag:    ctxtags.TagForProxyAuth,
		authFromRoute: func(route *kedge_map.Route) (auth.Source, bool) {
			return route.ProxyAuth, route.ProxyAuth != nil
		},
	}
}

func WrapForBackendAuth(parentTransport RoundTripper) RoundTripper {
	return &authTripper{
		parent:     parentTransport.Clone(),
		authHeader: authHeader,
		authTag:    ctxtags.TagForAuth,
		authFromRoute: func(route *kedge_map.Route) (auth.Source, bool) {
			return route.BackendAuth, route.BackendAuth != nil
		},
	}
}
