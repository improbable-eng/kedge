package tripperware

import (
	"net/http"
	"time"

	"github.com/mwitkow/go-httpwares/tags"
	"github.com/mwitkow/kedge/lib/auth"
	"github.com/mwitkow/kedge/lib/http/ctxtags"
	"github.com/mwitkow/kedge/lib/map"
	"github.com/pkg/errors"
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
	authTimeTag   string
	authFromRoute func(route *kedge_map.Route) (auth.Source, bool)
}

func (t *authTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	route, ok, err := getRoute(req.Context())
	if err != nil {
		return nil, errors.Wrap(err, "authTripper: Failed to get route from context")
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

	now := time.Now()
	val, err := authSource.HeaderValue()
	tags.Set(t.authTimeTag, time.Since(now).String())
	if err != nil {
		return nil, errors.Wrapf(err, "authTripper: Failed to get header value from authSource %s", authSource.Name())
	}

	req.Header.Set(t.authHeader, val)
	return t.parent.RoundTrip(req)
}

func WrapForProxyAuth(parentTransport http.RoundTripper) http.RoundTripper {
	return &authTripper{
		parent:      parentTransport,
		authHeader:  ProxyAuthHeader,
		authTag:     ctxtags.TagForProxyAuth,
		authTimeTag: ctxtags.TagForProxyAuthTime,
		authFromRoute: func(route *kedge_map.Route) (auth.Source, bool) {
			return route.ProxyAuth, route.ProxyAuth != nil
		},
	}
}

func WrapForBackendAuth(parentTransport http.RoundTripper) http.RoundTripper {
	return &authTripper{
		parent:      parentTransport,
		authHeader:  authHeader,
		authTag:     ctxtags.TagForAuth,
		authTimeTag: ctxtags.TagForBackendAuthTime,
		authFromRoute: func(route *kedge_map.Route) (auth.Source, bool) {
			return route.BackendAuth, route.BackendAuth != nil
		},
	}
}
