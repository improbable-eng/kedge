package backendpool

import (
	"net"

	"google.golang.org/grpc"
	"crypto/tls"
	"github.com/stretchr/testify/require"
	"path"
	"runtime"
	"testing"
	"google.golang.org/grpc/credentials"
	pb_be "github.com/mwitkow/kfe/_protogen/kfe/config/grpc/backends"
	pb_res "github.com/mwitkow/kfe/_protogen/kfe/config/common/resolvers"
	pb_testproto "github.com/mwitkow/go-grpc-middleware/testing/testproto"
	"github.com/mwitkow/go-grpc-middleware/testing"
	"time"
	"github.com/stretchr/testify/suite"
	"golang.org/x/net/context"
	"github.com/mwitkow/go-srvlb/srv"
	"gopkg.in/cheggaaa/pb.v1"
	"sync"
)

var backendResolutionDuration = 10 * time.Millisecond

var backendConfigs = []*pb_be.Backend{
	&pb_be.Backend{
		Name: "non_secure",
		Resolver: pb_be.Backend_Srv{
			Srv: &pb_res.SrvResolver{
				DnsName: "_grpc._tcp.nonsecure.backends.test.local",
			},
		},
	},
}

type localBackends struct {
	mu         sync.RWMutex
	resolvable int
	listeners  []net.Listener
	servers    []*grpc.Server
}

func (l *localBackends) addServer(t *testing.T, serverOpt ...grpc.ServerOption) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "must be able to allocate a port for localBackend")
	// This is the point where we hook up the interceptor
	server := grpc.NewServer(serverOpt...)
	pb_testproto.RegisterTestServiceServer(server, &grpc_testing.TestPingService{T: t})
	l.mu.Lock()
	l.servers = append(l.servers, server)
	l.listeners = append(l.listeners, listener)
	l.mu.Unlock()
	go func() {
		if err := server.Serve(listener); err != nil {
			t.Logf("listener %v server: %v", listener.Addr(), err)
		}
	}()
}

func (l *localBackends) setResolvableCount(count int) {
	l.mu.Lock()
	l.resolvable = count
	l.mu.Unlock()
}

func (l *localBackends) targets() (targets []*srv.Target) {
	l.mu.RLock()
	for i := 0; i < l.resolvable && i < len(l.listeners); i++ {
		targets = append(targets, &srv.Target{
			Ttl:      backendResolutionDuration,
			DialAddr: l.listeners[i].Addr().String(),
		})
	}
	defer l.mu.RUnlock()

}

func (l *localBackends) Close() error {
	for _, s := range l.servers {
		s.GracefulStop()
	}
	for _, l := range l.listeners {
		l.Close()
	}
	return nil
}

type BackendPoolIntegrationTestSuite struct {
	suite.Suite

	originalDialFunc    func(ctx context.Context, network, address string) (net.Conn, error)
	originalSrvResolver srv.Resolver
	localBackends       map[string]*localBackends
}

func (s *BackendPoolIntegrationTestSuite) SimpleCtx() context.Context {
	ctx, _ := context.WithTimeout(context.TODO(), 2*time.Second)
	return ctx
}

func (s *BackendPoolIntegrationTestSuite) TearDownSuite() {
	time.Sleep(10 * time.Millisecond)
	for _, be := range s.localBackends {
		be.Close()
	}
}

func getTestingCertsPath() string {
	_, callerPath, _, _ := runtime.Caller(0)
	return path.Join(path.Dir(callerPath), "..", "..", "misc")
}
