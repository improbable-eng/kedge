package director

import (
	"strings"

	"github.com/Bplotka/oidc/authorize"
	"github.com/grpc-ecosystem/go-grpc-middleware/auth"
	"github.com/grpc-ecosystem/go-grpc-middleware/tags"
	"github.com/grpc-ecosystem/go-grpc-middleware/util/metautils"
	"github.com/improbable-eng/kedge/pkg/grpcutils"
	"github.com/improbable-eng/kedge/pkg/kedge/common"
	"github.com/improbable-eng/kedge/pkg/kedge/grpc/backendpool"
	"github.com/improbable-eng/kedge/pkg/kedge/grpc/director/router"
	"github.com/mwitkow/grpc-proxy/proxy"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

// New builds a StreamDirector based off a backend pool and a router.
func New(pool backendpool.Pool, adhocRouter common.Addresser, grpcRouter router.Router) proxy.StreamDirector {
	return func(ctx context.Context, fullMethodName string) (context.Context, *grpc.ClientConn, error) {
		beName, err := grpcRouter.Route(ctx, fullMethodName)
		if err != nil {
			if err == router.ErrRouteNotFound {
				ipPort, err := adhocRouter.Address(metautils.ExtractIncoming(ctx).Get(":authority"))
				if err != nil {
					return ctx, nil, err
				}

				var opts []grpc.DialOption
				opts = append(opts,
					grpc.WithInsecure(),
					grpc.WithBlock(),
					grpc.WithAuthority(fullMethodName),
					grpc.WithCodec(proxy.Codec()),
				)
				cc, err := grpc.Dial(ipPort, opts...)
				if err != nil {
					return ctx, nil, errors.Wrap(err, "dial")
				}

				go func() {
					<-ctx.Done()
					cc.Close()
				}()

				return grpcutils.CloneIncomingToOutgoingMD(ctx), cc, err
			}
			return ctx, nil, err
		}

		grpc_ctxtags.Extract(ctx).Set("grpc.proxy.backend", beName)

		cc, err := pool.Conn(beName)
		return grpcutils.CloneIncomingToOutgoingMD(ctx), cc, err
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
