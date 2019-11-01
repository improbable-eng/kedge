package grpc_integration

import (
	context2 "context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net"
	"net/url"
	"path"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	grpc_auth "github.com/grpc-ecosystem/go-grpc-middleware/auth"
	"github.com/grpc-ecosystem/go-grpc-middleware/util/metautils"
	"github.com/improbable-eng/go-srvlb/srv"
	"github.com/improbable-eng/kedge/pkg/kedge/common"
	"github.com/improbable-eng/kedge/pkg/kedge/grpc/backendpool"
	kedge_grpc "github.com/improbable-eng/kedge/pkg/kedge/grpc/client"
	"github.com/improbable-eng/kedge/pkg/kedge/grpc/director"
	"github.com/improbable-eng/kedge/pkg/kedge/grpc/director/adhoc"
	"github.com/improbable-eng/kedge/pkg/kedge/grpc/director/router"
	kedge_map "github.com/improbable-eng/kedge/pkg/map"
	srvresolver "github.com/improbable-eng/kedge/pkg/resolvers/srv"
	kedge_config_common "github.com/improbable-eng/kedge/protogen/kedge/config/common"
	pb_res "github.com/improbable-eng/kedge/protogen/kedge/config/common/resolvers"
	pb_be "github.com/improbable-eng/kedge/protogen/kedge/config/grpc/backends"
	pb_route "github.com/improbable-eng/kedge/protogen/kedge/config/grpc/routes"
	"github.com/mwitkow/go-conntrack/connhelpers"
	"github.com/mwitkow/grpc-proxy/proxy"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/transport"
)

var (
	backendResolutionDuration = 10 * time.Millisecond

	backendConfigs = []*pb_be.Backend{
		&pb_be.Backend{
			Name: "non_secure",
			Resolver: &pb_be.Backend_Srv{
				Srv: &pb_res.SrvResolver{
					DnsName: "_grpc._tcp.nonsecure.backends.test.local",
				},
			},
		},
		&pb_be.Backend{
			Name: "secure",
			Resolver: &pb_be.Backend_Srv{
				Srv: &pb_res.SrvResolver{
					DnsName: "_grpctls._tcp.secure.backends.test.local",
				},
			},
			Security: &pb_be.Security{
				InsecureSkipVerify: true,
			},
		},
	}

	defaultBackendCount = 5

	routeConfigs = []*pb_route.Route{
		&pb_route.Route{
			BackendName:        "secure",
			ServiceNameMatcher: "hand_rolled.secure.*", // testservice is mwitkow.testproto
		},
		&pb_route.Route{
			BackendName:        "non_secure",
			ServiceNameMatcher: "hand_rolled.non_secure.*", // these will be used in unknownPingBackHandler-based tests
		},
		&pb_route.Route{
			BackendName:        "unspecified_backend",
			ServiceNameMatcher: "bad.backend.*", // bad.backend will match a bad tests
		},
		&pb_route.Route{
			BackendName:          "secure",
			ServiceNameMatcher:   "hand_rolled.common.*", // these will be used in unknownPingBackHandler-based tests
			AuthorityHostMatcher: "secure.ext.test.local",
		},
		&pb_route.Route{
			BackendName:          "non_secure",
			ServiceNameMatcher:   "hand_rolled.common.*",
			AuthorityHostMatcher: "non_secure.ext.test.local",
		},
	}

	adhocConfig = []*kedge_config_common.Adhoc{
		{
			DnsNameMatcher: "*nonsecure.backends.adhoctest.local",
			Port: &kedge_config_common.Adhoc_Port{
				AllowedRanges: []*kedge_config_common.Adhoc_Port_Range{
					{
						// This will be started on localhost. God knows what port it will be.
						From: 1024,
						To:   65535,
					},
				},
			},
		},
	}
)

type unknownResponse struct {
	Addr               string `protobuf:"bytes,1,opt,name=addr,json=value"`
	Method             string `protobuf:"bytes,2,opt,name=method"`
	Backend            string `protobuf:"bytes,3,opt,name=backend"`
	AuthorizationToken string `protobuf:"bytes,4,opt,name=auth"`
}

func (m *unknownResponse) Reset()         { *m = unknownResponse{} }
func (m *unknownResponse) String() string { return fmt.Sprintf("%v", *m) }
func (*unknownResponse) ProtoMessage()    {}

func unknownPingbackHandler(backendName string, serverAddr string) grpc.StreamHandler {
	return func(srv interface{}, stream grpc.ServerStream) error {
		tr, ok := transport.StreamFromContext(stream.Context())
		if !ok {
			return fmt.Errorf("handler should have access to transport info")
		}
		md := metautils.ExtractIncoming(stream.Context())
		return stream.SendMsg(&unknownResponse{Method: tr.Method(), Addr: serverAddr, Backend: backendName, AuthorizationToken: md.Get("authorization")})
	}
}

type localBackends struct {
	name       string
	mu         sync.RWMutex
	resolvable int
	listeners  []net.Listener
	servers    []*grpc.Server
}

func (l *localBackends) addServer(t *testing.T, serverOpt ...grpc.ServerOption) {
	listener, err := net.Listen("tcp", "127.0.0.1:")
	require.NoError(t, err, "must be able to allocate a port for localBackend")

	// This is the point where we hook up the test interceptor.
	serverOpt = append(serverOpt, grpc.UnknownServiceHandler(unknownPingbackHandler(l.name, listener.Addr().String())))
	server := grpc.NewServer(serverOpt...)
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

type testAuthorizer struct {
	expectedToken string
	returnErr     error
}

func (t *testAuthorizer) IsAuthorized(_ context2.Context, token string) error {
	if t.returnErr != nil {
		return t.returnErr
	}

	if token == t.expectedToken {
		return nil
	}
	return errors.New("Unauthenticated")
}

type BackendPoolIntegrationTestSuite struct {
	suite.Suite

	proxy         *grpc.Server
	proxyListener net.Listener
	pool          backendpool.Pool

	proxyConn           *grpc.ClientConn
	kedgeMapper         kedge_map.Mapper
	originalDialFunc    func(ctx context.Context, network, address string) (net.Conn, error)
	originalSrvResolver srv.Resolver
	originalAResolver   func(addr string) (names []string, err error)
	localBackends       map[string]*localBackends

	authorizer *testAuthorizer
}

func TestBackendPoolIntegrationTestSuite(t *testing.T) {
	defer leaktest.CheckTimeout(t, 10*time.Second)()

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

const testToken = "test-token"

type tokenCreds struct {
	token  string
	header string
}

func (c *tokenCreds) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	return map[string]string{
		c.header: c.token,
	}, nil
}

func (c *tokenCreds) RequireTransportSecurity() bool {
	return false
}

func (s *BackendPoolIntegrationTestSuite) SetupSuite() {
	var err error
	s.proxyListener, err = net.Listen("tcp", "localhost:0")
	require.NoError(s.T(), err, "must be able to allocate a port for proxyListener")
	// Make ourselves the resolver for SRV for our backends. See Lookup function.
	s.originalSrvResolver = srvresolver.ParentSrvResolver
	srvresolver.ParentSrvResolver = s

	// Make ourselves the A resolver for backends for the Addresser.
	s.originalAResolver, common.DefaultALookup = common.DefaultALookup, lookupAddr

	s.buildBackends()

	s.pool, err = backendpool.NewStatic(backendConfigs)
	require.NoError(s.T(), err, "backend pool creation must not fail")
	staticRouter := router.NewStatic(logrus.New(), routeConfigs)
	adhocAddresser := adhoc.NewStaticAddresser(adhocConfig)
	dir := director.New(s.pool, adhocAddresser, staticRouter)

	grpcAuth := director.NewGRPCAuthorizer(&testAuthorizer{expectedToken: testToken, returnErr: nil})
	s.proxy = grpc.NewServer(
		grpc.CustomCodec(proxy.Codec()),
		grpc.UnknownServiceHandler(proxy.TransparentHandler(dir)),
		grpc_middleware.WithUnaryServerChain(grpc_auth.UnaryServerInterceptor(grpcAuth)),
		grpc_middleware.WithStreamServerChain(grpc_auth.StreamServerInterceptor(grpcAuth)),
		grpc.Creds(credentials.NewTLS(s.tlsConfigForTest())),
	)

	go func() {
		s.T().Logf("starting proxy with TLS at: %v", s.proxyListener.Addr().String())
		s.proxy.Serve(s.proxyListener)
	}()
	proxyPort := s.proxyListener.Addr().String()[strings.LastIndex(s.proxyListener.Addr().String(), ":")+1:]
	proxyUrl, _ := url.Parse(fmt.Sprintf("https://localhost:%s", proxyPort))
	s.kedgeMapper = kedge_map.Single(proxyUrl)
	s.proxyConn, err = grpc.Dial(fmt.Sprintf("localhost:%s", proxyPort),
		grpc.WithTransportCredentials(credentials.NewTLS(s.tlsConfigForTest())),
		grpc.WithPerRPCCredentials(&tokenCreds{token: "bearer " + testToken, header: "proxy-authorization"}),
		grpc.WithBlock(),
	)
	require.NoError(s.T(), err, "dialing the proxy on a conn *must not* fail")
}

func (s *BackendPoolIntegrationTestSuite) buildBackends() {
	s.localBackends = make(map[string]*localBackends)
	nonSecure := &localBackends{name: "nonsecure_localbackends"}
	for i := 0; i < defaultBackendCount; i++ {
		nonSecure.addServer(s.T())
	}
	nonSecure.setResolvableCount(100)
	s.localBackends["_grpc._tcp.nonsecure.backends.test.local"] = nonSecure

	secure := &localBackends{name: "secure_localbackends"}
	for i := 0; i < defaultBackendCount; i++ {
		secure.addServer(s.T(), grpc.Creds(credentials.NewTLS(s.tlsConfigForTest())))
	}
	secure.setResolvableCount(100)
	s.localBackends["_grpctls._tcp.secure.backends.test.local"] = secure

	adhocBackends := &localBackends{name: "adhoc_localbackends"}
	for i := 0; i < defaultBackendCount; i++ {
		adhocBackends.addServer(s.T())
	}
	adhocBackends.setResolvableCount(100)
	s.localBackends["_grpc._tcp.nonsecure.backends.adhoctest.local"] = adhocBackends
}

func (s *BackendPoolIntegrationTestSuite) SimpleCtx() context.Context {
	ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
	return ctx
}

func (s *BackendPoolIntegrationTestSuite) TestCallToNonSecureBackend() {
	resp := &unknownResponse{}
	err := grpc.Invoke(s.SimpleCtx(), "/hand_rolled.non_secure.SomeService/Method", &unknownResponse{}, resp, s.proxyConn)
	require.NoError(s.T(), err, "no error on simple call")
	assert.Equal(s.T(), "/hand_rolled.non_secure.SomeService/Method", resp.Method)
	assert.Equal(s.T(), "nonsecure_localbackends", resp.Backend)
}

func (s *BackendPoolIntegrationTestSuite) TestCallToSecureBackend() {
	resp := &unknownResponse{}
	err := grpc.Invoke(s.SimpleCtx(), "/hand_rolled.secure.SomeService/Method", &unknownResponse{}, resp, s.proxyConn)
	require.NoError(s.T(), err, "no error on simple call")
	assert.Equal(s.T(), "/hand_rolled.secure.SomeService/Method", resp.Method)
	assert.Equal(s.T(), "secure_localbackends", resp.Backend)
}

func (s *BackendPoolIntegrationTestSuite) TestClientDialSecureToNonSecureBackend() {
	// This tests whether the DialThroughKedge passes the authority correctly
	cc, err := kedge_grpc.DialThroughKedge(
		context.TODO(),
		"secure.ext.test.local",
		s.tlsConfigForTest(),
		s.kedgeMapper, grpc.WithPerRPCCredentials(&tokenCreds{token: "bearer " + testToken, header: "proxy-authorization"}),
	)
	require.NoError(s.T(), err, "dialing through kedge must succeed")
	defer cc.Close()
	resp := s.invokeUnknownHandlerPingbackAndAssert("/hand_rolled.common.NonSpecificService/Method", cc)
	assert.Equal(s.T(), "secure_localbackends", resp.Backend)
}

func (s *BackendPoolIntegrationTestSuite) TestClientDialSecureToNonSecureBackend_BackendAuth() {
	cc, err := kedge_grpc.DialThroughKedge(
		context.TODO(),
		"secure.ext.test.local",
		s.tlsConfigForTest(),
		s.kedgeMapper,
		grpc.WithPerRPCCredentials(&tokenCreds{token: "bearer " + testToken, header: "proxy-authorization"}),
		grpc.WithPerRPCCredentials(&tokenCreds{token: "bearer test-backend-token", header: "authorization"}),
	)
	require.NoError(s.T(), err, "dialing through kedge must succeed")
	defer cc.Close()
	resp := s.invokeUnknownHandlerPingbackAndAssert("/hand_rolled.common.NonSpecificService/Method", cc)
	assert.Equal(s.T(), "secure_localbackends", resp.Backend)
	assert.Equal(s.T(), "bearer test-backend-token", resp.AuthorizationToken)
}

func (s *BackendPoolIntegrationTestSuite) invokeUnknownHandlerPingbackAndAssert(fullMethod string, conn *grpc.ClientConn) *unknownResponse {
	resp := &unknownResponse{}
	err := grpc.Invoke(s.SimpleCtx(), fullMethod, &unknownResponse{}, resp, conn)
	require.NoError(s.T(), err, "no error on call to unknown handler call")
	assert.Equal(s.T(), fullMethod, resp.Method)
	return resp
}

func (s *BackendPoolIntegrationTestSuite) TestCallToNonSecureBackendLoadBalancesRoundRobin() {
	backendResponse := make(map[string]int)
	for i := 0; i < defaultBackendCount*10; i++ {
		resp := s.invokeUnknownHandlerPingbackAndAssert("/hand_rolled.non_secure.SomeService/Method", s.proxyConn)
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
	err := grpc.Invoke(s.SimpleCtx(), "/bad.route.doesnt.exist/Method", &unknownResponse{}, &unknownResponse{}, s.proxyConn)
	require.EqualError(s.T(), err, "rpc error: code = Unimplemented desc = unknown route to service", "no error on simple call")
}

func (s *BackendPoolIntegrationTestSuite) TestClientDialToAdhocBackend() {
	// This tests whether the DialThroughKedge passes the authority correctly
	addr := s.localBackends["_grpc._tcp.nonsecure.backends.adhoctest.local"].targets()[0].DialAddr
	port := addr[strings.LastIndex(addr, ":")+1:]
	cc, err := kedge_grpc.DialThroughKedge(
		s.SimpleCtx(),
		fmt.Sprintf("nonsecure.backends.adhoctest.local:%v", port),
		s.tlsConfigForTest(),
		s.kedgeMapper, grpc.WithPerRPCCredentials(&tokenCreds{token: "bearer " + testToken, header: "proxy-authorization"}),
	)
	require.NoError(s.T(), err, "dialing through kedge must succeed")
	defer cc.Close()
	resp := s.invokeUnknownHandlerPingbackAndAssert("/hand_rolled.common.NonSpecificService/Method", cc)
	assert.Equal(s.T(), "adhoc_localbackends", resp.Backend)
}

func (s *BackendPoolIntegrationTestSuite) TestCallToUnknownBackend() {
	err := grpc.Invoke(s.SimpleCtx(), "/bad.backend.doesnt.exist/Method", &unknownResponse{}, &unknownResponse{}, s.proxyConn)
	require.EqualError(s.T(), err, "rpc error: code = Unimplemented desc = unknown backend", "no error on simple call")
}

func (s *BackendPoolIntegrationTestSuite) TearDownSuite() {
	s.proxyConn.Close()
	s.pool.Close()
	// Restore old resolver.
	if s.originalSrvResolver != nil {
		srvresolver.ParentSrvResolver = s.originalSrvResolver
	}
	if s.originalAResolver != nil {
		common.DefaultALookup = s.originalAResolver
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

// implements A resolver that always resolves local host.
func lookupAddr(addr string) (names []string, err error) {
	return []string{"127.0.0.1"}, nil
}

func getTestingCertsPath() string {
	_, callerPath, _, _ := runtime.Caller(0)
	return path.Join(path.Dir(callerPath), "..", "..", "..", "misc")
}
