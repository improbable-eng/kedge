package tripperware

import (
	"context"
	"net/http"
	"strings"

	"github.com/mwitkow/kedge/lib/map"
)

const routeContextKey = "proxy-route"

func getRoute(ctx context.Context) (*kedge_map.Route, bool) {
	r, ok := ctx.Value(routeContextKey).(*kedge_map.Route)
	return r, ok
}

type mappingTripper struct {
	mapper kedge_map.Mapper
	parent http.RoundTripper
}

func (t *mappingTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Host
	if strings.Contains(host, ":") {
		host = host[:strings.LastIndex(host, ":")]
	}

	route, err := t.mapper.Map(host)
	if err == kedge_map.ErrNotKedgeDestination {
		return t.parent.RoundTrip(req)
	}
	if err != nil {
		return nil, err
	}

	return t.parent.RoundTrip(
		req.WithContext(context.WithValue(req.Context(), routeContextKey, route)),
	)
}

func (t *mappingTripper) Clone() RoundTripper {
	return &(*t)
}

func WrapForMapping(mapper kedge_map.Mapper, parentTransport RoundTripper) RoundTripper {
	return &mappingTripper{mapper: mapper, parent: parentTransport.Clone()}
}
