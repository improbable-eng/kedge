package grpc_winch

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"path"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/util/metautils"
	"github.com/mwitkow/go-conntrack/connhelpers"
	"github.com/mwitkow/grpc-proxy/proxy"
	pb "github.com/improbable-eng/kedge/protogen/winch/config"
	"github.com/improbable-eng/kedge/lib/map"
	"github.com/improbable-eng/kedge/winch/lib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/transport"
)

type unknownResponse struct {
	Addr        string `protobuf:"bytes,1,opt,name=addr,json=value"`
	Method      string `protobuf:"bytes,2,opt,name=method"`
	Backend     string `protobuf:"bytes,3,opt,name=backend"`
	ProxyAuth   string `protobuf:"bytes,4,opt,name=proxyauth"`
	BackendAuth string `protobuf:"bytes,5,opt,name=backendauth"`
}

func (m *unknownResponse) Reset()         { *m = unknownResponse{} }
func (m *unknownResponse) String() string { return fmt.Sprintf("%v", m) }
func (*unknownResponse) ProtoMessage()    {}

func unknownPingbackHandler(backendName string, serverAddr string) grpc.StreamHandler {
	return func(srv interface{}, stream grpc.ServerStream) error {
		tr, ok := transport.StreamFromContext(stream.Context())
		if !ok {
			return fmt.Errorf("handler should have access to transport info")
		}
		md := metautils.ExtractIncoming(stream.Context())
		return stream.SendMsg(
			&unknownResponse{
				Method:      tr.Method(),
				Addr:        serverAddr,
				Backend:     backendName,
				BackendAuth: md.Get("authorization"),
				ProxyAuth:   md.Get("proxy-authorization"),
			})
	}
}

type localKedges struct {
	listeners []net.Listener
	servers   []*grpc.Server
}

func buildAndStartServer(t *testing.T, config *tls.Config, name string) (net.Listener, *grpc.Server) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "must be able to allocate a port for localBackend")
	// This is the point where we hook up the interceptor
	server := grpc.NewServer(
		grpc.UnknownServiceHandler(unknownPingbackHandler(name, listener.Addr().String())),
		grpc.Creds(credentials.NewTLS(config)),
	)

	go func() {
		server.Serve(listener)
	}()

	return listener, server
}

func (l *localKedges) SetupKedges(t *testing.T, config *tls.Config, num int) {
	for i := 0; i < num; i++ {
		listener, server := buildAndStartServer(t, config, fmt.Sprintf("%d", i))
		l.servers = append(l.servers, server)
		l.listeners = append(l.listeners, listener)
	}
}

func (l *localKedges) Close() error {
	for _, lis := range l.listeners {
		lis.Close()
	}
	for _, s := range l.servers {
		s.GracefulStop()
	}
	return nil
}

type WinchIntegrationSuite struct {
	suite.Suite

	winch              *grpc.Server
	winchListenerPlain net.Listener
	routes             *winch.StaticRoutes

	localSecureKedges localKedges
}

func TestWinchIntegrationSuite(t *testing.T) {
	suite.Run(t, &WinchIntegrationSuite{})
}

func moveToLocalhost(addr string) string {
	return strings.Replace(addr, "127.0.0.1", "localhost", -1)
}

func (s *WinchIntegrationSuite) SetupSuite() {
	var err error
	s.winchListenerPlain, err = net.Listen("tcp", "localhost:0")
	require.NoError(s.T(), err, "must be able to allocate a port for winchListenerPlain")

	// It does not make sense if kedge is not secure.
	s.localSecureKedges.SetupKedges(s.T(), s.tlsConfigForTest(), 3)

	testConfig := &pb.MapperConfig{
		Routes: []*pb.Route{
			{
				BackendAuth: "access1",
				Type: &pb.Route_Direct{
					Direct: &pb.DirectRoute{
						Key: "resource1.ext.example.com",
						Url: "https://" + moveToLocalhost(s.localSecureKedges.listeners[0].Addr().String()),
					},
				},
			},
			{
				ProxyAuth: "proxy-access1",
				Type: &pb.Route_Direct{
					Direct: &pb.DirectRoute{
						Key: "resource2.ext.example.com",
						Url: "https://" + moveToLocalhost(s.localSecureKedges.listeners[1].Addr().String()),
					},
				},
			},
			{
				BackendAuth: "access2",
				Type: &pb.Route_Regexp{
					Regexp: &pb.RegexpRoute{
						Exp: "([a-z0-9-].*).(?P<cluster>[a-z0-9-].*).internal.example.com",
						Url: "https://" + moveToLocalhost(s.localSecureKedges.listeners[2].Addr().String()),
					},
				},
			},
			{
				BackendAuth: "error-access",
				Type: &pb.Route_Direct{
					Direct: &pb.DirectRoute{
						Key: "error.ext.example.com",
						Url: "https://" + moveToLocalhost(s.localSecureKedges.listeners[1].Addr().String()),
					},
				},
			},
		},
	}
	authConfig := &pb.AuthConfig{
		AuthSources: []*pb.AuthSource{
			{
				Name: "access1",
				Type: &pb.AuthSource_Dummy{
					Dummy: &pb.DummyAccess{
						Value: "test-token",
					},
				},
			},
			{
				Name: "access2",
				Type: &pb.AuthSource_Dummy{
					Dummy: &pb.DummyAccess{
						Value: "user:password",
					},
				},
			},
			{
				Name: "proxy-access1",
				Type: &pb.AuthSource_Dummy{
					Dummy: &pb.DummyAccess{
						Value: "proxy-test-token",
					},
				},
			},
			{
				Name: "error-access",
				Type: &pb.AuthSource_Dummy{
					Dummy: &pb.DummyAccess{
						Value: "", // No value will trigger error on Token() (inside auth tripper)
					},
				},
			},
		},
	}

	s.routes, err = winch.NewStaticRoutes(winch.NewAuthFactory(s.winchListenerPlain.Addr().String(), http.NewServeMux()), testConfig, authConfig)
	require.NoError(s.T(), err, "config must be parsable")

	s.winch = grpc.NewServer(
		grpc.CustomCodec(proxy.Codec()),
		grpc.UnknownServiceHandler(proxy.TransparentHandler(
			New(kedge_map.RouteMapper(s.routes.Get()), s.tlsConfigForTest())),
		),
	)

	go func() {
		s.winch.Serve(s.winchListenerPlain)
	}()
}

func (s *WinchIntegrationSuite) SimpleCtx() context.Context {
	ctx, _ := context.WithTimeout(context.TODO(), 5*time.Second)
	return ctx
}

// dialThroughWinch creates plain, insecure, local connection to winch.
func (s *WinchIntegrationSuite) dialThroughWinch(targetAuthority string) (*grpc.ClientConn, error) {
	proxyPort := s.winchListenerPlain.Addr().String()[strings.LastIndex(s.winchListenerPlain.Addr().String(), ":")+1:]

	return grpc.Dial(
		fmt.Sprintf("localhost:%s", proxyPort),
		grpc.WithBlock(),
		grpc.WithInsecure(),
		grpc.WithAuthority(targetAuthority),
	)
}

func (s *WinchIntegrationSuite) TestCallKedgeThroughWinch_NoRoute() {
	cc, err := s.dialThroughWinch("resourceXXX.ext.example.com")
	require.NoError(s.T(), err, "dialing the winch *must not* fail")
	defer func() {
		cc.Close()
		time.Sleep(10 * time.Millisecond)
	}()

	resp := &unknownResponse{}
	// ServiceName/Method does not matter here. We are now matching based on authority only.
	err = grpc.Invoke(s.SimpleCtx(), "/test.SomeService/Method", &unknownResponse{}, resp, cc)
	require.EqualError(s.T(), err, "rpc error: code = Unimplemented desc = not a kedge destination", "error on simple call")
}

func (s *WinchIntegrationSuite) TestCallKedgeThroughWinch_DirectRoute_ValidAuth() {
	cc, err := s.dialThroughWinch("resource1.ext.example.com")
	require.NoError(s.T(), err, "dialing the winch *must not* fail")
	defer func() {
		cc.Close()
		time.Sleep(10 * time.Millisecond)
	}()

	resp := &unknownResponse{}
	err = grpc.Invoke(s.SimpleCtx(), "/test.SomeService/Method", &unknownResponse{}, resp, cc)
	require.NoError(s.T(), err, "no error on simple call")
	assert.Equal(s.T(), "/test.SomeService/Method", resp.Method)
	assert.Equal(s.T(), "0", resp.Backend)
	assert.Equal(s.T(), "", resp.ProxyAuth)
	assert.Equal(s.T(), "bearer test-token", resp.BackendAuth)
}

func (s *WinchIntegrationSuite) TestCallKedgeThroughWinch_DirectRoute2_ProxyAuth() {
	cc, err := s.dialThroughWinch("resource2.ext.example.com")
	require.NoError(s.T(), err, "dialing the winch *must not* fail")
	defer func() {
		cc.Close()
		time.Sleep(10 * time.Millisecond)
	}()

	resp := &unknownResponse{}
	err = grpc.Invoke(s.SimpleCtx(), "/test.SomeService/Method", &unknownResponse{}, resp, cc)
	require.NoError(s.T(), err, "no error on simple call")
	assert.Equal(s.T(), "/test.SomeService/Method", resp.Method)
	assert.Equal(s.T(), "1", resp.Backend)
	assert.Equal(s.T(), "bearer proxy-test-token", resp.ProxyAuth)
	assert.Equal(s.T(), "", resp.BackendAuth)
}

func (s *WinchIntegrationSuite) TestCallKedgeThroughWinch_RegexpRoute_ValidAuth() {
	cc, err := s.dialThroughWinch("service1.ab1-prod.internal.example.com")
	require.NoError(s.T(), err, "dialing the winch *must not* fail")
	defer func() {
		cc.Close()
		time.Sleep(10 * time.Millisecond)
	}()

	resp := &unknownResponse{}
	err = grpc.Invoke(s.SimpleCtx(), "/test.SomeService/Method", &unknownResponse{}, resp, cc)
	require.NoError(s.T(), err, "no error on simple call")
	assert.Equal(s.T(), "/test.SomeService/Method", resp.Method)
	assert.Equal(s.T(), "2", resp.Backend)
	assert.Equal(s.T(), "", resp.ProxyAuth)
	assert.Equal(s.T(), "bearer user:password", resp.BackendAuth)
}

func (s *WinchIntegrationSuite) TestCallKedgeThroughWinch_DirectRoute3_AuthError() {
	cc, err := s.dialThroughWinch("error.ext.example.com")
	require.NoError(s.T(), err, "dialing the winch *must not* fail")
	defer func() {
		cc.Close()
		time.Sleep(10 * time.Millisecond)
	}()

	resp := &unknownResponse{}
	// ServiceName/Method does not matter here. We are now matching based on authority only.
	err = grpc.Invoke(s.SimpleCtx(), "/test.SomeService/Method", &unknownResponse{}, resp, cc)
	require.EqualError(s.T(), err, "rpc error: code = Internal desc = transport: cannot get token: Error dummy auth source. No TokenValue specified")
}

func (s *WinchIntegrationSuite) TearDownSuite() {
	if s.winch != nil {
		s.winch.GracefulStop()
		s.winchListenerPlain.Close()
	}
	s.localSecureKedges.Close()
}

func (s *WinchIntegrationSuite) tlsConfigForTest() *tls.Config {
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
	return path.Join(path.Dir(callerPath), "..", "..", "..", "misc")
}
