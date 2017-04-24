package router

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/mwitkow/kedge/http/director/proxyreq"
	"google.golang.org/grpc/metadata"

	"sync"

	pb "github.com/mwitkow/kedge/_protogen/kedge/config/http/routes"
)

var (
	emptyMd          = metadata.Pairs()
	ErrRouteNotFound = errors.New("unknown route to service")
)

type Router interface {
	// Route returns a backend name for a given call, or an error.
	// Note: the request *must* be normalized.
	Route(req *http.Request) (backendName string, err error)
}

type dynamic struct {
	mu           sync.RWMutex
	staticRouter *static
}

// NewDynamic creates a new dynamic router that can be have its routes updated.
func NewDynamic() *dynamic {
	return &dynamic{staticRouter: NewStatic([]*pb.Route{})}
}

func (d *dynamic) Route(req *http.Request) (backendName string, err error) {
	d.mu.RLock()
	staticRouter := d.staticRouter
	d.mu.RUnlock()
	return staticRouter.Route(req)
}

// Update sets the routing table to the provided set of routes.
func (d *dynamic) Update(routes []*pb.Route) {
	staticRouter := NewStatic(routes)
	d.mu.Lock()
	d.staticRouter = staticRouter
	d.mu.Unlock()
}

type static struct {
	routes []*pb.Route
}

func NewStatic(routes []*pb.Route) *static {
	return &static{routes: routes}
}

func (r *static) Route(req *http.Request) (backendName string, err error) {
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
	return "", ErrRouteNotFound
}

func (r *static) urlMatches(u *url.URL, matchers []string) bool {
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

func (r *static) hostMatches(host string, matcher string) bool {
	if host == "" {
		return false // we can't handle empty hosts
	}
	if matcher == "" {
		return true // no matcher set, match all like a boss!
	}
	return host == matcher
}

func (r *static) headersMatch(header http.Header, expectedKv map[string]string) bool {
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

func (r *static) requestTypeMatch(requestMode proxyreq.ProxyMode, routeMode pb.ProxyMode) bool {
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
