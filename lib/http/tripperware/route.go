package tripperware

import (
	"net/http"

	"github.com/mwitkow/go-httpwares/tags"
	"github.com/mwitkow/kedge/lib/http/ctxtags"
)

// routingTripper is a piece of tripperware that dials certain destinations (indicated by a route stored in request's context) through a remote proxy (kedge).
// The dialing is performed in a "reverse proxy" fashion, where the Host header indicates to the kedge what backend
// to dial.
// NOTE: It requires to have mappingTripper before itself to put the routing inside context.
type routingTripper struct {
	parent http.RoundTripper
}

func (t *routingTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	host := getURLHost(req)

	route, ok, err := getRoute(req.Context())
	if err != nil {
		return nil, err
	}
	if !ok {
		return t.parent.RoundTrip(req)
	}

	destURL := route.URL

	tags := http_ctxtags.ExtractInbound(req)
	tags.Set(ctxtags.TagForProxyDestURL, destURL)
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

func WrapForRouting(parentTransport http.RoundTripper) http.RoundTripper {
	return &routingTripper{parent: parentTransport}
}
