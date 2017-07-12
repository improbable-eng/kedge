package tripperware

import (
	"net/http"
	"strings"

	"github.com/mwitkow/go-httpwares/tags"
)

const authHeader = "Authorization"

// backendAuthTripper is a piece of tripperware that injects auth defined per route to authorize request for end-backend application.
// It is done via Auth mapper.
// NOTE: This is required due to restrictions for common CLIs to not sending auth headers over plain HTTP. Since CLI and
// local winch are on the same machine, we don't want to introduce overhead of HTTPS (and on-fly cert generation).
// NOTE: It requires to have mappingTripper before itself to put the routing inside context.
type backendAuthTripper struct {
	parent http.RoundTripper
}

func (t *backendAuthTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.Host
	if strings.Contains(host, ":") {
		host = host[:strings.LastIndex(host, ":")]
	}

	route, ok := getRoute(req.Context())
	if !ok {
		return t.parent.RoundTrip(req)
	}

	authSource := route.BackendAuth
	if authSource == nil {
		// No auth configured.
		return t.parent.RoundTrip(req)
	}

	tags := http_ctxtags.ExtractInbound(req)
	tags.Set("http.auth", authSource.Name())

	val, err := authSource.HeaderValue()
	if err != nil {
		return nil, err
	}
	// TODO(bplotka): Consider not overwriting auth is it was passed somehow (even despite the fact it was plain HTTP)
	req.Header.Set(authHeader, val)
	return t.parent.RoundTrip(req)
}

func (t *backendAuthTripper) Clone() RoundTripper {
	return &(*t)
}

func WrapForBackendAuth(parentTransport RoundTripper) RoundTripper {
	return &backendAuthTripper{parent: parentTransport.Clone()}
}
