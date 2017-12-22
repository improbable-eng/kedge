package director

import (
	"strings"

	"github.com/Bplotka/oidc/authorize"
	"github.com/grpc-ecosystem/go-grpc-middleware/auth"
	"github.com/grpc-ecosystem/go-grpc-middleware/tags"
	"github.com/grpc-ecosystem/go-grpc-middleware/util/metautils"
	"github.com/improbable-eng/kedge/pkg/grpc/metadata"
	"github.com/improbable-eng/kedge/pkg/kedge/grpc/backendpool"
	"github.com/improbable-eng/kedge/pkg/kedge/grpc/director/router"
	"github.com/mwitkow/grpc-proxy/proxy"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

// New builds a StreamDirector based off a backend pool and a router.
func New(pool backendpool.Pool, router router.Router) proxy.StreamDirector {
	return func(ctx context.Context, fullMethodName string) (context.Context, *grpc.ClientConn, error) {
		beName, err := router.Route(ctx, fullMethodName)
		if err != nil {
			return ctx, nil, err
		}

		ctx = grpc_metadata.CloneIncomingToOutgoing(ctx)

		grpc_ctxtags.Extract(ctx).Set("grpc.proxy.backend", beName)
		cc, err := pool.Conn(beName)
		return ctx, cc, err
	}
}

// NewGRPCAuthorizer builds a grpc_auth.AuthFunc that checks authorization header from gRPC request.
func NewGRPCAuthorizer(authorizer authorize.Authorizer) grpc_auth.AuthFunc {
	return func(ctx context.Context) (context.Context, error) {
		token, err := authFromMD(ctx)
		if err != nil {
			return ctx, err
		}

		return ctx, authorizer.IsAuthorized(ctx, token)
	}
}

func authFromMD(ctx context.Context) (string, error) {
	val := metautils.ExtractIncoming(ctx).Get("proxy-authorization")
	if val == "" {
		return "", grpc.Errorf(codes.Unauthenticated, "Request unauthenticated. No proxy-authorization header")

	}
	splits := strings.SplitN(val, " ", 2)
	if len(splits) != 2 {
		return "", grpc.Errorf(codes.Unauthenticated, "Bad authorization string")
	}
	if strings.ToLower(splits[0]) != "bearer" {
		return "", grpc.Errorf(codes.Unauthenticated, "Request unauthenticated. Not bearer type")
	}
	return splits[1], nil
}
