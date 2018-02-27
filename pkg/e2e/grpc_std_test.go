package e2e

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/improbable-eng/kedge/protogen/e2e"
	"github.com/improbable-eng/thanos/pkg/runutil"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestGRPCEndpointCall invokes end backend SayHello RPC through winch and kedge.
func TestGRPCEndpointCallViaEnv(t *testing.T) {
	t.Skip("")
	const name = "kedge"

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	exit, err := spinup(t, ctx, config{winch: true, kedge: true, testEndpoint: true})
	if err != nil {
		t.Error(err)
		cancel()
		return
	}

	defer func() {
		cancel()
		<-exit
	}()

	err = runutil.Retry(time.Second, ctx.Done(), func() error {
		if err = assertRunning(exit); err != nil {
			t.Error(err)
			return nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		reply, err := grpcHelloViaWinchAndKedgeUsingEnv(ctx, endpointDNS, name)
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
}

func grpcHelloViaWinchAndKedgeUsingEnv(ctx context.Context, dnsName string, name string) (*e2e_helloworld.HelloReply, error) {
	os.Setenv("http_proxy", fmt.Sprintf("http://127.0.0.1:%s", grpcWinchPort))
	os.Setenv("HTTP_PROXY", fmt.Sprintf("http://127.0.0.1:%s", grpcWinchPort))


	cc, err := grpc.DialContext(ctx, fmt.Sprintf("%s:%s", dnsName, grpcTestEndpointPort), grpc.WithInsecure())
	if err != nil {
		return nil, err
	}

	return e2e_helloworld.NewGreeterClient(cc).SayHello(ctx, &e2e_helloworld.HelloRequest{Name: name})
}
