package router

import (
	pb "github.com/improbable-eng/kedge/protogen/kedge/config/grpc/routes"

	"strings"

	"sync"

	"github.com/grpc-ecosystem/go-grpc-middleware/util/metautils"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
)

var (
	emptyMd       = metadata.Pairs()
	routeNotFound = grpc.Errorf(codes.Unimplemented, "unknown route to service")
)

// Router is an interface that decides what backend a given stream should be directed to.
type Router interface {
	// Route returns a backend name for a given call, or an error.
	Route(ctx context.Context, fullMethodName string) (backendName string, err error)
}

type dynamic struct {
	mu           sync.RWMutex
	staticRouter *static
}

// NewDynamic creates a new dynamic router that can be have its routes updated.
func NewDynamic() *dynamic {
	return &dynamic{staticRouter: NewStatic([]*pb.Route{})}
}

func (d *dynamic) Route(ctx context.Context, fullMethodName string) (backendName string, err error) {
	d.mu.RLock()
	staticRouter := d.staticRouter
	d.mu.RUnlock()
	return staticRouter.Route(ctx, fullMethodName)
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

func (r *static) Route(ctx context.Context, fullMethodName string) (backendName string, err error) {
	md := metautils.ExtractIncoming(ctx)
	if strings.HasPrefix(fullMethodName, "/") {
		fullMethodName = fullMethodName[1:]
	}
	for _, route := range r.routes {
		if !r.serviceNameMatches(fullMethodName, route.ServiceNameMatcher) {
			continue
		}
		if !r.authorityMatches(md, route.AuthorityMatcher) {
			continue
		}
		if !r.metadataMatches(md, route.MetadataMatcher) {
			continue
		}
		return route.BackendName, nil
	}
	return "", routeNotFound
}

func (r *static) serviceNameMatches(fullMethodName string, matcher string) bool {
	if matcher == "" || matcher == "*" {
		return true
	}
	if matcher[len(matcher)-1] == '*' {
		return strings.HasPrefix(fullMethodName, matcher[0:len(matcher)-1])
	}
	return fullMethodName == matcher
}

func (r *static) authorityMatches(md metautils.NiceMD, matcher string) bool {
	if matcher == "" {
		return true
	}
	auth := md.Get(":authority")
	if auth == "" {
		return false // there was no authority header and it was expected
	}
	return auth == matcher
}

func (r *static) metadataMatches(md metautils.NiceMD, expectedKv map[string]string) bool {
	for expK, expV := range expectedKv {
		vals, ok := md[strings.ToLower(expK)]
		if !ok {
			return false // key doesn't exist
		}
		found := false
		for _, v := range vals {
			if v == expV {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
