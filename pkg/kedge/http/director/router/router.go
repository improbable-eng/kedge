package router

import (
	"errors"
	"net/http"
	"sync"

	"github.com/improbable-eng/kedge/pkg/kedge/http/director/matcher"
	pb "github.com/improbable-eng/kedge/protogen/kedge/config/http/routes"
)

var (
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

type route struct {
	*matcher.Matcher
	backendName string
}

type static struct {
	routes []route
}

func NewStatic(routes []*pb.Route) *static {
	var staticRoutes []route
	for _, r := range routes {
		staticRoutes = append(staticRoutes, route{
			Matcher:     matcher.New(r.Matcher),
			backendName: r.BackendName,
		})
	}
	return &static{routes: staticRoutes}
}

func (r *static) Route(req *http.Request) (backendName string, err error) {
	for _, r := range r.routes {
		if r.Match(req) {
			return r.backendName, nil
		}
	}
	return "", ErrRouteNotFound
}
