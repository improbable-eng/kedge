package tripperware

import (
	"context"
	"net/http"

	"github.com/mwitkow/kedge/lib/map"
	"github.com/pkg/errors"
)

// routeContextKey specifies the key that route is stored inside request's context.
// if no route is found the nil route is stored.
const routeContextKey = "proxy-route"

func getRoute(ctx context.Context) (*kedge_map.Route, bool, error) {
	r, ok := ctx.Value(routeContextKey).(*kedge_map.Route)
	if !ok {
		// Unit tests should catch that.
		return nil, false, errors.New("InternalError: Tripperware misconfiguration. MappingTripper was not before current tripper.")
	}
	return r, r != nil, nil
}

type mappingTripper struct {
	mapper kedge_map.Mapper
	parent http.RoundTripper
}

func requestWithRoute(req *http.Request, r *kedge_map.Route) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), routeContextKey, r))
}

func (t *mappingTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Get routing based on request Host and optional port.
	route, err := t.mapper.Map(req.URL.Hostname(), req.URL.Port())
	if err == kedge_map.ErrNotKedgeDestination {
		return t.parent.RoundTrip(
			// We store nil to ensure that we can catch case when mappingTripper is not in the chain before other trippers
			// that requires stored route.
			requestWithRoute(req, nil),
		)
	}
	if err != nil {
		return nil, errors.Wrapf(err, "mappingTripper: Failed to map host %s and port %s into route", req.URL.Hostname(), req.URL.Port())
	}

	return t.parent.RoundTrip(
		requestWithRoute(req, route),
	)
}

func WrapForMapping(mapper kedge_map.Mapper, parentTransport http.RoundTripper) http.RoundTripper {
	return &mappingTripper{mapper: mapper, parent: parentTransport}
}
