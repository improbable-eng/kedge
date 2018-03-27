package kedge_map

import (
	"net/url"

	"github.com/improbable-eng/kedge/pkg/tokenauth"
)

// Mapper is an interface that allows you to direct traffic to different kedges including various auth.
// These are used by client libraries.
type Mapper interface {
	// Map maps a target's DNS name and optional port (e.g. myservice.prod.ext.europe-cluster.local and 80) to a Route.
	// If the targets shouldn't be proxied, ErrNotKedgeDestination should be returned.
	Map(targetAuthorityDnsName string, port string) (*Route, error)
}

type Route struct {
	// URL specifies public URL to the Kedge fronting route destination.
	// The returned Scheme is deciding whether the Kedge connection is secure.
	URL *url.URL

	// BackendAuth represents optional auth for end application. Sometimes it is required to be injected here, because of common
	// restriction blocking auth headers in plain HTTP requests (even when communication locally with local forward proxy).
	BackendAuth tokenauth.Source
	// ProxyAuth represents optional auth for kedge.
	ProxyAuth tokenauth.Source
}

// ErrorKedgeDestination displays that host/port is not a kedge destination. We are not using errors.New, because sometimes we need
// to check for type of the error (for example in winch/grpc/proxy.go).
type ErrorNotKedgeDestination struct {
	host string
	port string
}

func NotKedgeDestinationErr(host string, port string) error {
	return &ErrorNotKedgeDestination{host: host, port: port}
}

func (e *ErrorNotKedgeDestination) Error() string {
	msg := e.host
	if e.port != "" {
		msg += ":" + e.port
	}
	return msg + " is not a kedge destination"
}
