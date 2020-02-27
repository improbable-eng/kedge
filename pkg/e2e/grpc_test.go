package e2e

import (
	"fmt"
	"net/url"
	"testing"
	"time"

	e2e_helloworld "github.com/improbable-eng/kedge/protogen/e2e"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestGRPCEndpointCall invokes end backend SayHello RPC through winch and kedge using authentication in the backend.
func TestGRPCEndpointCallWithBackendAuth(t *testing.T) {
	const name = "kedge"

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	exit, err := spinup(t, ctx, config{winch: true, kedge: true, testEndpoint: true, testEndpointAuthentication: true})
	if err != nil {
		t.Error(err)
		cancel()
		return
	}

	defer func() {
		cancel()
		<-exit
	}()

	t.Run("AuthenticatedBackendAuthRequest_ShouldSucceed", func(t *testing.T) {
		err = Retry(time.Second, ctx.Done(), func() error {
			if err = assertRunning(exit); err != nil {
				t.Error(err)
				return nil
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			reply, err := grpcHelloViaWinchAndKedge(ctx, endpointDNS, name)
			if err != nil {
				if st, ok := status.FromError(err); ok {
					if st.Code() == codes.Unavailable {
						return errors.New("not ready")
					}
					t.Errorf("Unexpected gRPC error: %v", st.Code())
					return nil
				}

				return err
			}

			if reply.Message != expectedResponse(name) {
				t.Errorf("Unexpected response: %v; Exp: %s", reply.Message, expectedResponse(name))
				return nil
			}

			return nil
		})
		require.NoError(t, err)
	})

	t.Run("UnauthenticatedBackendAuthRequest_ShouldReturnUnauthenticated", func(t *testing.T) {
		// Try URL for which winch will not append backend auth and we expect it to fail.
		_, err = grpcHelloViaWinchAndKedge(ctx, noAuthEndpointDNS, name)
		require.Error(t, err)

		if st, ok := status.FromError(err); ok {
			if st.Code() != codes.Unauthenticated {
				t.Errorf("Unexpected gRPC code: %v. Expected 401.", st.Code())
				return
			}
			// Ok.
			return
		}
		t.Errorf("Unexpected error: %v", err)
	})
}

// TestGRPCEndpointCall invokes end backend SayHello RPC through winch and kedge using authentication in the proxy.
func TestGRPCEndpointCallWithProxyAuth(t *testing.T) {
	const name = "kedge"

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	exit, err := spinup(t, ctx, config{winch: true, kedge: true, kedgeBearerTokenAuth: true, testEndpoint: true, testEndpointAuthentication: false})
	if err != nil {
		t.Error(err)
		cancel()
		return
	}

	defer func() {
		cancel()
		<-exit
	}()

	t.Run("AuthenticatedProxyAuthRequest_ShouldSucceed", func(t *testing.T) {
		err = Retry(time.Second, ctx.Done(), func() error {
			if err = assertRunning(exit); err != nil {
				t.Error(err)
				return nil
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			reply, err := grpcHelloViaWinchAndKedge(ctx, bearerTokenProxyAuthEndpointDNS, name)
			if err != nil {
				if st, ok := status.FromError(err); ok {
					if st.Code() == codes.Unavailable {
						return errors.New("not ready")
					}
					t.Errorf("Unexpected gRPC error: %v", st.Code())
					return nil
				}

				return err
			}

			if reply.Message != expectedResponse(name) {
				t.Errorf("Unexpected response: %v; Exp: %s", reply.Message, expectedResponse(name))
				return nil
			}

			return nil
		})
		require.NoError(t, err)
	})

	t.Run("UnauthenticatedBackendAuthRequest_ShouldReturnUnauthenticated", func(t *testing.T) {
		// Try URL for which winch will not append backend auth and we expect it to fail.
		_, err = grpcHelloViaWinchAndKedge(ctx, wrongBearerTokenProxyAuthEndpointDNS, name)
		require.Error(t, err)

		if st, ok := status.FromError(err); ok {
			if st.Code() != codes.Unauthenticated {
				t.Errorf("Unexpected gRPC code: %v. Expected 401.", st.Code())
				return
			}
			// Ok.
			return
		}
		t.Errorf("Unexpected error: %v", err)
	})
}

func grpcHelloViaWinchAndKedge(ctx context.Context, dnsName string, name string) (*e2e_helloworld.HelloReply, error) {
	dialer, err := winchDialer(fmt.Sprintf("http://127.0.0.1:%s", grpcWinchPort))
	if err != nil {
		return nil, err
	}

	cc, err := dialer(ctx, fmt.Sprintf("%s:%s", dnsName, grpcTestEndpointPort))
	if err != nil {
		return nil, err
	}

	return e2e_helloworld.NewGreeterClient(cc).SayHello(ctx, &e2e_helloworld.HelloRequest{Name: name})
}

type dialContextFunc func(context.Context, string, ...grpc.DialOption) (*grpc.ClientConn, error)

func winchDialer(winchURL string) (dialContextFunc, error) {
	proxyURL, err := url.Parse(winchURL)
	if err != nil {
		return nil, errors.Wrapf(err, "invalid winch URL address %q: %v", winchURL, err)
	}

	return func(ctx context.Context, targetAuthority string, grpcOpts ...grpc.DialOption) (*grpc.ClientConn, error) {
		// NOTE: This will conflict with TLS transport credential grpc options passed by argument, but we don't have the control to validate that.
		grpcOpts = append(grpcOpts, grpc.WithInsecure())
		grpcOpts = append(grpcOpts, grpc.WithAuthority(targetAuthority))

		return grpc.DialContext(ctx, proxyURL.Host, grpcOpts...)
	}, nil
}
