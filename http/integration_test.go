package grpc_integration

import (
	"net"

	"crypto/tls"
	"crypto/x509"
	"path"
	"runtime"
	"sync"
	"testing"
	"time"

	"io/ioutil"

	"github.com/mwitkow/go-conntrack/connhelpers"
	"github.com/mwitkow/go-grpc-middleware/testing"
	pb_testproto "github.com/mwitkow/go-grpc-middleware/testing/testproto"
	"github.com/mwitkow/go-srvlb/srv"
	"github.com/mwitkow/grpc-proxy/proxy"
	pb_res "github.com/mwitkow/kedge/_protogen/kedge/config/common/resolvers"
	pb_be "github.com/mwitkow/kedge/_protogen/kedge/config/http/backends"
	pb_route "github.com/mwitkow/kedge/_protogen/kedge/config/http/routes"

	"fmt"

	"strings"

	"github.com/mwitkow/kedge/http/backendpool"
	"github.com/mwitkow/kedge/http/director"
	"github.com/mwitkow/kedge/http/director/router"
	"github.com/mwitkow/kedge/lib/resolvers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/transport"
)

var backendResolutionDuration = 10 * time.Millisecond

var backendConfigs = []*pb_be.Backend{
	&pb_be.Backend{
		Name: "non_secure",
		Resolver: &pb_be.Backend_Srv{
			Srv: &pb_res.SrvResolver{
				DnsName: "_grpc._tcp.nonsecure.backends.test.local",
			},
		},
	},
}

var defaultBackendCount = 5

var routeConfigs = []*pb_route.Route{
	&pb_route.Route{
		BackendName:        "non_secure",
		ServiceNameMatcher: "mwitkow.*", // testservice is mwitkow.testproto
	},
	&pb_route.Route{
		BackendName:        "non_secure",
		ServiceNameMatcher: "hand_rolled.non_secure.*", // these will be used in unknownPingBackHandler-based tests
	},
	&pb_route.Route{
		BackendName:        "unspecified_backend",
		ServiceNameMatcher: "bad.backend.*", // bad.backend will match a bad tests
	},
}

type unknownResponse struct {
	Addr   string `protobuf:"bytes,1,opt,name=addr,json=value"`
	Method string `protobuf:"bytes,2,opt,name=method"`
}

func (m *unknownResponse) Reset()         { *m = unknownResponse{} }
func (m *unknownResponse) String() string { return fmt.Sprintf("%v", m) }
func (*unknownResponse) ProtoMessage()    {}

func unknownPingbackHandler(serverAddr string) grpc.StreamHandler {
	return func(srv interface{}, stream grpc.ServerStream) error {
		tr, ok := transport.StreamFromContext(stream.Context())
		if !ok {
			return fmt.Errorf("handler should have access to transport info")
		}
		return stream.SendMsg(&unknownResponse{Method: tr.Method(), Addr: serverAddr})
	}
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
	serverOpt = append(serverOpt, grpc.UnknownServiceHandler(unknownPingbackHandler(listener.Addr().String())))
	server := grpc.NewServer(serverOpt...)
	pb_testproto.RegisterTestServiceServer(server, &grpc_testing.TestPingService{T: t})
	l.mu.Lock()
	l.servers = append(l.servers, server)
	l.listeners = append(l.listeners, listener)
	l.mu.Unlock()
	go func() {
		server.Serve(listener)
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
	return targets
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

	proxy         *grpc.Server
	proxyListener net.Listener
	pool          backendpool.Pool

	proxyConn           *grpc.ClientConn
	originalDialFunc    func(ctx context.Context, network, address string) (net.Conn, error)
	originalSrvResolver srv.Resolver
	localBackends       map[string]*localBackends
}

func TestBackendPoolIntegrationTestSuite(t *testing.T) {
	suite.Run(t, &BackendPoolIntegrationTestSuite{})
}

// implements srv resolver.
func (s *BackendPoolIntegrationTestSuite) Lookup(domainName string) ([]*srv.Target, error) {
	local, ok := s.localBackends[domainName]
	if !ok {
		return nil, fmt.Errorf("Unknown local backend '%v' in testing", domainName)
	}
	return local.targets(), nil
}

func (s *BackendPoolIntegrationTestSuite) SetupSuite() {
	var err error
	s.proxyListener, err = net.Listen("tcp", "localhost:0")
	require.NoError(s.T(), err, "must be able to allocate a port for proxyListener")
	// Make ourselves the resolver for SRV for our backends. See Lookup function.
	s.originalSrvResolver = resolvers.ParentSrvResolver
	resolvers.ParentSrvResolver = s
	s.buildBackends()

	s.pool, err = backendpool.NewStatic(backendConfigs)
	require.NoError(s.T(), err, "backend pool creation must not fail")
	router := router.NewStatic(routeConfigs)
	dir := director.New(s.pool, router)

	s.proxy = grpc.NewServer(
		grpc.CustomCodec(proxy.Codec()),
		grpc.UnknownServiceHandler(proxy.TransparentHandler(dir)),
		grpc.Creds(credentials.NewTLS(s.tlsConfigForTest())),
	)

	go func() {
		s.T().Logf("starting proxy with TLS at: %v", s.proxyListener.Addr().String())
		s.proxy.Serve(s.proxyListener)
	}()
	proxyPort := s.proxyListener.Addr().String()[strings.LastIndex(s.proxyListener.Addr().String(), ":"):]
	s.proxyConn, err = grpc.Dial(fmt.Sprintf("localhost%s", proxyPort),
		grpc.WithTransportCredentials(credentials.NewTLS(s.tlsConfigForTest())),
		grpc.WithBlock(),
	)
	require.NoError(s.T(), err, "dialing the proxy on a conn *must not* fail")
}

func (s *BackendPoolIntegrationTestSuite) buildBackends() {
	s.localBackends = make(map[string]*localBackends)
	nonSecure := &localBackends{}
	for i := 0; i < defaultBackendCount; i++ {
		nonSecure.addServer(s.T())
	}
	nonSecure.setResolvableCount(100)
	s.localBackends["_grpc._tcp.nonsecure.backends.test.local"] = nonSecure
}

func (s *BackendPoolIntegrationTestSuite) SimpleCtx() context.Context {
	ctx, _ := context.WithTimeout(context.TODO(), 2*time.Second)
	return ctx
}

func (s *BackendPoolIntegrationTestSuite) TestCallToNonSecureBackend() {
	client := pb_testproto.NewTestServiceClient(s.proxyConn)
	_, err := client.Ping(s.SimpleCtx(), &pb_testproto.PingRequest{})
	require.NoError(s.T(), err, "no error on simple call")
}

func (s *BackendPoolIntegrationTestSuite) TestCallToNonSecureBackendLoadBalancesRoundRobin() {
	backendResponse := make(map[string]int)
	for i := 0; i < defaultBackendCount*10; i++ {
		resp := &unknownResponse{}
		err := grpc.Invoke(s.SimpleCtx(), "/hand_rolled.non_secure.SomeService/Method", &pb_testproto.Empty{}, resp, s.proxyConn)
		require.NoError(s.T(), err, "unknownPingHandler should not return errors")
		if _, ok := backendResponse[resp.Addr]; ok {
			backendResponse[resp.Addr] += 1
		} else {
			backendResponse[resp.Addr] = 1
		}
	}
	assert.Len(s.T(), backendResponse, defaultBackendCount, "requests should hit all backends")
	for addr, value := range backendResponse {
		assert.Equal(s.T(), 10, value, "backend %v should have received the same amount of requests", addr)
	}
}

func (s *BackendPoolIntegrationTestSuite) TestCallToUnknownRouteCausesError() {
	err := grpc.Invoke(s.SimpleCtx(), "/bad.route.doesnt.exist/Method", &pb_testproto.Empty{}, &pb_testproto.Empty{}, s.proxyConn)
	require.EqualError(s.T(), err, "rpc error: code = 12 desc = unknown route to service", "no error on simple call")
}

func (s *BackendPoolIntegrationTestSuite) TestCallToUnknownBackend() {
	err := grpc.Invoke(s.SimpleCtx(), "/bad.backend.doesnt.exist/Method", &pb_testproto.Empty{}, &pb_testproto.Empty{}, s.proxyConn)
	require.EqualError(s.T(), err, "rpc error: code = 12 desc = unknown backend", "no error on simple call")
}

func (s *BackendPoolIntegrationTestSuite) TearDownSuite() {
	s.proxyConn.Close()
	s.pool.Close()
	// Restore old resolver.
	if s.originalSrvResolver != nil {
		resolvers.ParentSrvResolver = s.originalSrvResolver
	}
	time.Sleep(10 * time.Millisecond)
	if s.proxy != nil {
		s.proxy.GracefulStop()
		s.proxyListener.Close()
	}
	for _, be := range s.localBackends {
		be.Close()
	}
}

func (s *BackendPoolIntegrationTestSuite) tlsConfigForTest() *tls.Config {
	tlsConfig, err := connhelpers.TlsConfigForServerCerts(
		path.Join(getTestingCertsPath(), "localhost.crt"),
		path.Join(getTestingCertsPath(), "localhost.key"))
	if err != nil {
		require.NoError(s.T(), err, "failed reading server certs")
	}
	tlsConfig.RootCAs = x509.NewCertPool()
	// Make Client cert verification an option.
	tlsConfig.ClientCAs = x509.NewCertPool()
	tlsConfig.ClientAuth = tls.VerifyClientCertIfGiven
	data, err := ioutil.ReadFile(path.Join(getTestingCertsPath(), "ca.crt"))
	if err != nil {
		s.FailNow("Failed reading CA: %v", err)
	}
	if ok := tlsConfig.RootCAs.AppendCertsFromPEM(data); !ok {
		s.FailNow("failed processing CA file")
	}
	if ok := tlsConfig.ClientCAs.AppendCertsFromPEM(data); !ok {
		s.FailNow("failed processing CA file")
	}
	return tlsConfig
}

func getTestingCertsPath() string {
	_, callerPath, _, _ := runtime.Caller(0)
	return path.Join(path.Dir(callerPath), "..", "misc")
}
