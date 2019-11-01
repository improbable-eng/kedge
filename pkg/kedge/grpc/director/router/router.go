package router

import (
	"strconv"
	"strings"
	"sync"

	grpc_ctxtags "github.com/grpc-ecosystem/go-grpc-middleware/tags"
	"github.com/grpc-ecosystem/go-grpc-middleware/util/metautils"
	pb "github.com/improbable-eng/kedge/protogen/kedge/config/grpc/routes"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var (
	emptyMd          = metadata.Pairs()
	ErrRouteNotFound = status.Errorf(codes.Unimplemented, "unknown route to service")
)

// Router is an interface that decides what backend a given stream should be directed to.
type Router interface {
	// Route returns a backend name for a given call, or an error.
	Route(ctx context.Context, fullMethodName string) (backendName string, err error)
}

type dynamic struct {
	logger       logrus.FieldLogger
	mu           sync.RWMutex
	staticRouter *static
}

// NewDynamic creates a new dynamic router that can be have its routes updated.
func NewDynamic(logger logrus.FieldLogger) *dynamic {
	return &dynamic{logger: logger, staticRouter: NewStatic(logger, []*pb.Route{})}
}

func (d *dynamic) Route(ctx context.Context, fullMethodName string) (backendName string, err error) {
	d.mu.RLock()
	staticRouter := d.staticRouter
	d.mu.RUnlock()
	return staticRouter.Route(ctx, fullMethodName)
}

// Update sets the routing table to the provided set of routes.
func (d *dynamic) Update(routes []*pb.Route) {
	staticRouter := NewStatic(d.logger, routes)
	d.mu.Lock()
	d.staticRouter = staticRouter
	d.mu.Unlock()
}

type static struct {
	logger logrus.FieldLogger
	routes []*pb.Route
}

func NewStatic(logger logrus.FieldLogger, routes []*pb.Route) *static {
	return &static{logger: logger, routes: routes}
}

func (r *static) Route(ctx context.Context, fullMethodName string) (backendName string, err error) {
	md := metautils.ExtractIncoming(ctx)

	tags := grpc_ctxtags.Extract(ctx)
	tags.Set("grpc.target.authority", md.Get(":authority"))

	if strings.HasPrefix(fullMethodName, "/") {
		fullMethodName = fullMethodName[1:]
	}
	for _, route := range r.routes {
		if !r.serviceNameMatches(fullMethodName, route.ServiceNameMatcher) {
			continue
		}
		if !r.authorityHostMatches(md, route.AuthorityHostMatcher) {
			continue
		}
		if !r.authorityPortMatches(md, route.AuthorityPortMatcher) {
			continue
		}
		if !r.metadataMatches(md, route.MetadataMatcher) {
			continue
		}
		return route.BackendName, nil
	}
	return "", ErrRouteNotFound
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

func (r *static) authorityHostMatches(md metautils.NiceMD, hostMatcher string) bool {
	if hostMatcher == "" {
		return true
	}
	auth := md.Get(":authority")
	if auth == "" {
		return false // there was no authority header and it was expected
	}

	return stripPort(auth) == hostMatcher
}

func (r *static) authorityPortMatches(md metautils.NiceMD, portMatcher uint32) bool {
	if portMatcher == 0 {
		return true
	}
	auth := md.Get(":authority")
	if auth == "" {
		return false // there was no authority header and it was expected
	}

	portUint, err := strconv.ParseUint(portOnly(auth), 10, 32)
	if err != nil {
		r.logger.WithError(err).WithField("authority", auth).Error("Unexpected gRPC request :authority port format.")
		return false
	}

	return uint32(portUint) == portMatcher
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

func stripPort(hostport string) string {
	colon := strings.IndexByte(hostport, ':')
	if colon == -1 {
		return hostport
	}
	if i := strings.IndexByte(hostport, ']'); i != -1 {
		return strings.TrimPrefix(hostport[:i], "[")
	}
	return hostport[:colon]
}

func portOnly(hostport string) string {
	colon := strings.IndexByte(hostport, ':')
	if colon == -1 {
		return ""
	}
	if i := strings.Index(hostport, "]:"); i != -1 {
		return hostport[i+len("]:"):]
	}
	if strings.Contains(hostport, "]") {
		return ""
	}
	return hostport[colon+len(":"):]
}
