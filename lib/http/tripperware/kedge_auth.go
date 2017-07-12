package tripperware

import (
	"net/http"
	"strings"

	"github.com/mwitkow/go-httpwares/tags"
)

const proxyAuthHeader = "Proxy-Authorization"

// kedgeAuthTripper is a piece of tripperware that injects auth defined per route to authorize request for kedge.
// NOTE: It requires to have mappingTripper before itself to put the routing inside context.
type kedgeAuthTripper struct {
	parent http.RoundTripper
}

func (t *kedgeAuthTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Host
	if strings.Contains(host, ":") {
		host = host[:strings.LastIndex(host, ":")]
	}

	route, ok := getRoute(req.Context())
	if !ok {
		return t.parent.RoundTrip(req)
	}

	authSource := route.ProxyAuth
	if authSource == nil {
		// No auth configured.
		return t.parent.RoundTrip(req)
	}

	tags := http_ctxtags.ExtractInbound(req)
	tags.Set("http.proxy.auth", authSource.Name())

	val, err := authSource.HeaderValue()
	if err != nil {
		return nil, err
	}

	req.Header.Set(proxyAuthHeader, val)
	return t.parent.RoundTrip(req)
}

func (t *kedgeAuthTripper) Clone() RoundTripper {
	return &(*t)
}

func WrapForKedgeAuth(parentTransport RoundTripper) RoundTripper {
	return &kedgeAuthTripper{parent: parentTransport.Clone()}
}
