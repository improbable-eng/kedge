package router

import (
	pb "github.com/mwitkow/kedge/_protogen/kedge/config/http/routes"

	"strings"

	"net/http"
	"net/url"

	"errors"

	"github.com/mwitkow/kedge/http/director/proxyreq"
	"google.golang.org/grpc/metadata"
)

var (
	emptyMd       = metadata.Pairs()
	routeNotFound = errors.New("unknown route to service")
)

type Router interface {
	// Route returns a backend name for a given call, or an error.
	// Note: the request *must* be normalized.
	Route(req *http.Request) (backendName string, err error)
}

type router struct {
	routes []*pb.Route
}

func NewStatic(routes []*pb.Route) *router {
	return &router{routes: routes}
}

func (r *router) Route(req *http.Request) (backendName string, err error) {
	for _, route := range r.routes {
		if !r.urlMatches(req.URL, route.PathRules) {
			continue
		}
		if !r.hostMatches(req.URL.Host, route.HostMatcher) {
			continue
		}
		if !r.headersMatch(req.Header, route.HeaderMatcher) {
			continue
		}
		if !r.requestTypeMatch(proxyreq.GetProxyMode(req), route.ProxyMode) {
			continue
		}
		return route.BackendName, nil
	}
	return "", routeNotFound
}

func (r *router) urlMatches(u *url.URL, matchers []string) bool {
	if len(matchers) == 0 {
		return true
	}
	for _, m := range matchers {
		if m == "" {
			continue
		}
		if m[len(m)-1] == '*' {
			if strings.HasPrefix(u.Path, m[0:len(m)-1]) {
				return true
			}
		}
		if m == u.Path {
			return true
		}
	}
	return false
}

func (r *router) hostMatches(host string, matcher string) bool {
	if host == "" {
		return false // we can't handle empty hosts
	}
	if matcher == "" {
		return true // no matcher set, match all like a boss!
	}
	return host == matcher
}

func (r *router) headersMatch(header http.Header, expectedKv map[string]string) bool {
	for expK, expV := range expectedKv {
		headerVal := header.Get(expK)
		if headerVal == "" {
			return false // key doesn't exist
		}
		if headerVal != expV {
			return false
		}
	}
	return true
}

func (r *router) requestTypeMatch(requestMode proxyreq.ProxyMode, routeMode pb.ProxyMode) bool {
	if routeMode == pb.ProxyMode_ANY {
		return true
	}
	if requestMode == proxyreq.MODE_FORWARD_PROXY && routeMode == pb.ProxyMode_FORWARD_PROXY {
		return true
	}
	if requestMode == proxyreq.MODE_REVERSE_PROXY && routeMode == pb.ProxyMode_REVERSE_PROXY {
		return true
	}
	return false
}
