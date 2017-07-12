package kedge_map

import (
	"errors"
	"net/url"

	"github.com/mwitkow/kedge/lib/auth"
)

var (
	ErrNotKedgeDestination = errors.New("not a kedge destination")
)

// Mapper is an interface that allows you to direct traffic to different kedges including various auth.
// These are used by client libraries.
type Mapper interface {
	// Map maps a target's DNS name (e.g. myservice.prod.ext.europe-cluster.local) to a Route.
	// If the targets shouldn't be proxied, ErrNotKedgeDestination should be returned.
	Map(targetAuthorityDnsName string) (*Route, error)
}

type Route struct {
	// URL specifies public URL to the Kedge fronting route destination.
	// The returned Scheme is deciding whether the Kedge connection is secure.
	URL *url.URL

	// BackendAuth represents optional auth for end application. Sometimes it is required to be injected here, because of common
	// restriction blocking auth headers in plain HTTP requests (even when communication locally with local forward proxy).
	BackendAuth auth.Source
	// ProxyAuth represents optional auth for kedge.
	ProxyAuth auth.Source
}
