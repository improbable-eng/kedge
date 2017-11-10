package director

import (
	"github.com/grpc-ecosystem/go-grpc-middleware/tags"
	"github.com/mwitkow/grpc-proxy/proxy"
	"github.com/improbable-eng/kedge/grpc/backendpool"
	"github.com/improbable-eng/kedge/grpc/director/router"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

// New builds a StreamDirector based off a backend pool and a router.
func New(pool backendpool.Pool, router router.Router) proxy.StreamDirector {
	return func(ctx context.Context, fullMethodName string) (*grpc.ClientConn, error) {
		beName, err := router.Route(ctx, fullMethodName)
		if err != nil {
			return nil, err
		}
		grpc_ctxtags.Extract(ctx).Set("grpc.proxy.backend", beName)
		cc, err := pool.Conn(beName)
		if err != nil {
			return nil, err
		}
		return cc, nil
	}
}
