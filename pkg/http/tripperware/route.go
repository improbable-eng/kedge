package tripperware

import (
	"net/http"

	"github.com/improbable-eng/go-httpwares/tags"
	"github.com/improbable-eng/kedge/pkg/http/ctxtags"
	"github.com/pkg/errors"
)

// routingTripper is a piece of tripperware that dials certain destinations (indicated by a route stored in request's context) through a remote proxy (kedge).
// The dialing is performed in a "reverse proxy" fashion, where the Host header indicates to the kedge what backend
// to dial.
// NOTE: It requires to have mappingTripper before itself to put the routing inside context.
type routingTripper struct {
	parent http.RoundTripper
}

func (t *routingTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	route, ok, err := getRoute(req.Context())
	if err != nil {
		return nil, errors.Wrap(err, "routingTripper: Failed to get route from context")
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
	copyReq.Host = req.URL.Host               // store the original host (+ optional :port)
	return t.parent.RoundTrip(copyReq)
}

func WrapForRouting(parentTransport http.RoundTripper) http.RoundTripper {
	return &routingTripper{parent: parentTransport}
}
