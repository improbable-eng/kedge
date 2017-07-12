package tripperware

import (
	"net/http"
	"strings"

	"github.com/mwitkow/go-httpwares/tags"
)

// routingTripper is a piece of tripperware that dials certain destinations (indicated by a route stored in request's context) through a remote proxy (kedge).
// The dialing is performed in a "reverse proxy" fashion, where the Host header indicates to the kedge what backend
// to dial.
// NOTE: It requires to have mappingTripper before itself to put the routing inside context.
type routingTripper struct {
	parent http.RoundTripper
}

func (t *routingTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.Host
	if strings.Contains(host, ":") {
		host = host[:strings.LastIndex(host, ":")]
	}

	route, ok := getRoute(req.Context())
	if !ok || route == nil {
		return t.parent.RoundTrip(req)
	}

	destURL := route.URL

	tags := http_ctxtags.ExtractInbound(req)
	tags.Set("http.proxy.kedge_url", destURL)
	tags.Set(http_ctxtags.TagForHandlerName, destURL)

	// Copy the URL and the request to not override the callers info.
	copyUrl := *req.URL
	copyUrl.Scheme = destURL.Scheme
	copyUrl.Host = destURL.Host
	copyReq := req.WithContext(req.Context()) // makes a copy
	copyReq.URL = &(copyUrl)                  // shallow copy
	copyReq.Host = host                       // store the original host
	return t.parent.RoundTrip(copyReq)
}

func (t *routingTripper) Clone() RoundTripper {
	return &(*t)
}

func WrapForRouting(parentTransport RoundTripper) RoundTripper {
	return &routingTripper{parent: parentTransport.Clone()}
}
