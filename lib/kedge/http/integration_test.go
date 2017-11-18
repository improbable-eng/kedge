// Integration tests for the HTTP dispatching part of kedge
//
// These integration tests check the client, the routing and the backend handling for HTTP backends.
// It defines a set of backends (in `backendConfigs`), routes (in `routesConfig`), and adhoc configs (in `adhocConfigs`)
// that are used in a single `HttpProxyingIntegrationSuite`.
//
// That `HttpProxyingIntegrationSuite` defines two components: a proxy under test and a set of `localBackends`.
// These `localBackends` are HTTP(S) server that respond with `unknownPingbackHandler` to requests. They are resolved
// using a fake srv.Resolver implemented by `HttpProxyingIntegrationSuite`, and that's the same srv.Resolver
// that the proxy is using to resolve stuff from its `backendConfigs`.
//
// Tests consist of HTTP requests to different routes, and verification whether the proxy performed the expected operation.
// This also tests the official dialing client.
package http_integration

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	"github.com/go-chi/chi"
	"github.com/improbable-eng/go-srvlb/srv"
	pb_res "github.com/improbable-eng/kedge/protogen/kedge/config/common/resolvers"
	pb_be "github.com/improbable-eng/kedge/protogen/kedge/config/http/backends"
	pb_route "github.com/improbable-eng/kedge/protogen/kedge/config/http/routes"
	"github.com/improbable-eng/kedge/lib/kedge/http/backendpool"
	"github.com/improbable-eng/kedge/lib/kedge/http/client"
	"github.com/improbable-eng/kedge/lib/kedge/http/director"
	"github.com/improbable-eng/kedge/lib/kedge/http/director/adhoc"
	"github.com/improbable-eng/kedge/lib/kedge/http/director/router"
	"github.com/improbable-eng/kedge/lib/http/header"
	"github.com/improbable-eng/kedge/lib/map"
	"github.com/improbable-eng/kedge/lib/reporter"
	"github.com/improbable-eng/kedge/lib/resolvers/srv"
	"github.com/mwitkow/go-conntrack/connhelpers"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	testProxyAuthValue = "Bearer proxy-auth-secret"
	testToken          = "proxy-auth-secret"
)

var (
	backendResolutionDuration = 10 * time.Millisecond

	backendConfigs = []*pb_be.Backend{
		&pb_be.Backend{
			Name: "non_secure",
			Resolver: &pb_be.Backend_Srv{
				Srv: &pb_res.SrvResolver{
					DnsName: "_http._tcp.nonsecure.backends.test.local",
				},
			},
			Balancer: pb_be.Balancer_ROUND_ROBIN,
		},
		&pb_be.Backend{
			Name: "secure",
			Resolver: &pb_be.Backend_Srv{
				Srv: &pb_res.SrvResolver{
					DnsName: "_https._tcp.secure.backends.test.local",
				},
			},
			Security: &pb_be.Security{
				InsecureSkipVerify: true, // TODO(mwitkow): Add config TLS once we do parsing of TLS configs.
			},
			Balancer: pb_be.Balancer_ROUND_ROBIN,
		},
		&pb_be.Backend{
			Name: "killer",
			Resolver: &pb_be.Backend_Srv{
				Srv: &pb_res.SrvResolver{
					DnsName: "_https._tcp.nonsecure.killerbackend.test.local",
				},
			},
			Balancer: pb_be.Balancer_ROUND_ROBIN,
		},
	}

	nonSecureBackendCount = 5
	secureBackendCount    = 10

	routeConfigs = []*pb_route.Route{
		&pb_route.Route{
			BackendName: "non_secure",
			PathRules:   []string{"/some/strict/path"},
			HostMatcher: "nonsecure.ext.withport.example.com",
			PortMatcher: 81,
			ProxyMode:   pb_route.ProxyMode_REVERSE_PROXY,
		},
		&pb_route.Route{
			BackendName: "non_secure",
			PathRules:   []string{"/some/strict/path"},
			HostMatcher: "nonsecure.ext.withoutport.example.com",
			PortMatcher: 80,
			ProxyMode:   pb_route.ProxyMode_REVERSE_PROXY,
		},
		&pb_route.Route{
			BackendName: "non_secure",
			PathRules:   []string{"/some/strict/path"},
			HostMatcher: "nonsecure.ext.example.com",
			ProxyMode:   pb_route.ProxyMode_REVERSE_PROXY,
		},
		&pb_route.Route{
			BackendName: "non_secure",
			HostMatcher: "nonsecure.backends.test.local",
			ProxyMode:   pb_route.ProxyMode_FORWARD_PROXY,
		},
		&pb_route.Route{
			BackendName: "secure",
			PathRules:   []string{"/some/strict/path"},
			HostMatcher: "secure.ext.example.com",
			ProxyMode:   pb_route.ProxyMode_REVERSE_PROXY,
		},
		&pb_route.Route{
			BackendName: "secure",
			HostMatcher: "secure.backends.test.local",
			ProxyMode:   pb_route.ProxyMode_FORWARD_PROXY,
		},
		&pb_route.Route{
			BackendName: "killer",
			HostMatcher: "nonsecure.killerbackend.test.local",
			ProxyMode:   pb_route.ProxyMode_REVERSE_PROXY,
		},
	}

	adhocConfig = []*pb_route.Adhoc{
		{
			DnsNameMatcher: "*.pods.test.local",
			Port: &pb_route.Adhoc_Port{
				AllowedRanges: []*pb_route.Adhoc_Port_Range{
					{
						// This will be started on local host. God knows what port it will be.
						From: 1024,
						To:   65535,
					},
				},
			},
		},
	}
)

func unknownPingbackHandler(serverAddr string) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		resp.Header().Set("content-type", "application/json")
		resp.Header().Set("x-test-req-proto", fmt.Sprintf("%d.%d", req.ProtoMajor, req.ProtoMinor))
		resp.Header().Set("x-test-req-url", req.URL.String())
		resp.Header().Set("x-test-req-host", req.Host)
		resp.Header().Set("x-test-backend-addr", serverAddr)
		resp.Header().Set("x-test-auth-value", req.Header.Get("Authorization"))
		resp.Header().Set("x-test-proxy-auth-value", req.Header.Get("Proxy-Authorization"))
		resp.WriteHeader(http.StatusAccepted) // accepted to make sure stuff is slightly different.
		resp.Write([]byte("TEST"))
	})
}

type localBackends struct {
	mu         sync.RWMutex
	resolvable int
	listeners  []net.Listener
	servers    []*http.Server
}

func buildAndStartServer(t *testing.T, config *tls.Config) (net.Listener, *http.Server) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "must be able to allocate a port for localBackend")
	prefix := "nonsecure backend: "
	if config != nil {
		prefix = "secure backend: "
		listener = tls.NewListener(listener, config)
	}

	server := &http.Server{
		Handler:  unknownPingbackHandler(listener.Addr().String()),
		ErrorLog: log.New(os.Stderr, prefix, 0),
	}
	go func() {
		server.Serve(listener)
	}()
	return listener, server
}

func (l *localBackends) addServer(t *testing.T, config *tls.Config) {
	listener, server := buildAndStartServer(t, config)
	l.mu.Lock()
	l.servers = append(l.servers, server)
	l.listeners = append(l.listeners, listener)
	l.mu.Unlock()
}

func killConnectionHandler() http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		hi := resp.(http.Hijacker)
		conn, _, _ := hi.Hijack()
		conn.Close() // EOF!
	})
}

func (l *localBackends) addKillerServer(t *testing.T, config *tls.Config) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "must be able to allocate a port for localBackend")
	prefix := "nonsecure backend: "
	if config != nil {
		prefix = "secure backend: "
		listener = tls.NewListener(listener, config)
	}

	server := &http.Server{
		Handler:  killConnectionHandler(),
		ErrorLog: log.New(os.Stderr, prefix, 0),
	}
	go func() {
		server.Serve(listener)
	}()

	l.mu.Lock()
	l.servers = append(l.servers, server)
	l.listeners = append(l.listeners, listener)
	l.mu.Unlock()
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
	for _, lis := range l.listeners {
		lis.Close()
	}
	for _, s := range l.servers {
		s.Close()
	}
	return nil
}

type testAuthorizer struct {
	expectedToken string
	returnErr     error
}

func (t *testAuthorizer) IsAuthorized(_ context.Context, token string) error {
	if t.returnErr != nil {
		return t.returnErr
	}

	if token == t.expectedToken {
		return nil
	}
	return errors.New("Unauthenticated")
}

type HttpProxyingIntegrationSuite struct {
	suite.Suite

	proxy              *http.Server
	proxyListenerPlain net.Listener
	proxyListenerTls   net.Listener

	mapper              kedge_map.Mapper
	originalSrvResolver srv.Resolver
	originalAResolver   func(addr string) (names []string, err error)

	localBackends map[string]*localBackends
	authorizer    *testAuthorizer

	kedgeClient *http.Client
	backendPool backendpool.Pool
}

func TestBackendPoolIntegrationTestSuite(t *testing.T) {
	defer leaktest.CheckTimeout(t, 10*time.Second)()

	suite.Run(t, &HttpProxyingIntegrationSuite{})
}

// implements srv resolver.
func (s *HttpProxyingIntegrationSuite) Lookup(domainName string) ([]*srv.Target, error) {
	local, ok := s.localBackends[domainName]
	if !ok {
		return nil, fmt.Errorf("Unknown local backend '%v' in testing", domainName)
	}
	return local.targets(), nil
}

// implements A resolver that always resolves local host.
func (s *HttpProxyingIntegrationSuite) LookupAddr(addr string) (names []string, err error) {
	return []string{"127.0.0.1"}, nil
}

func (s *HttpProxyingIntegrationSuite) SetupSuite() {
	var err error
	s.proxyListenerPlain, err = net.Listen("tcp", "localhost:0")
	require.NoError(s.T(), err, "must be able to allocate a port for proxyListenerPlain")
	s.proxyListenerTls, err = net.Listen("tcp", "localhost:0")
	require.NoError(s.T(), err, "must be able to allocate a port for proxyListener")
	proxyTlsConfig, err := connhelpers.TlsConfigWithHttp2Enabled(s.tlsConfigForTest())
	require.NoError(s.T(), err, "no error when turning the proxy listening config into an http2 thingy")
	s.proxyListenerTls = tls.NewListener(s.proxyListenerTls, proxyTlsConfig)

	// Make ourselves the resolver for SRV for our backends. See Lookup function.
	s.originalSrvResolver = srvresolver.ParentSrvResolver
	srvresolver.ParentSrvResolver = s
	// Make ourselves the A resolver for backends for the Addresser.
	s.originalAResolver = adhoc.DefaultALookup
	adhoc.DefaultALookup = s.LookupAddr

	s.buildBackends()

	s.backendPool, err = backendpool.NewStatic(backendConfigs)
	require.NoError(s.T(), err, "backend pool creation must not fail")
	staticRouter := router.NewStatic(routeConfigs)
	addresser := adhoc.NewStaticAddresser(adhocConfig)
	s.authorizer = &testAuthorizer{}
	// Proxy with auth.
	s.proxy = &http.Server{
		Handler: chi.Chain(
			reporter.Middleware(logrus.New()),
			director.AuthMiddleware(s.authorizer),
		).Handler(director.New(s.backendPool, staticRouter, addresser, logrus.New())),
	}

	proxyPort := s.proxyListenerTls.Addr().String()[strings.LastIndex(s.proxyListenerTls.Addr().String(), ":")+1:]
	proxyUrl, _ := url.Parse(fmt.Sprintf("https://localhost:%s", proxyPort))
	s.mapper = kedge_map.Single(proxyUrl)

	go func() {
		s.proxy.Serve(s.proxyListenerPlain)
	}()
	go func() {
		s.proxy.Serve(s.proxyListenerTls)
	}()

	s.kedgeClient = kedge_http.NewClient(s.mapper, s.tlsConfigForTest(), http.DefaultTransport.(*http.Transport))
}

func (s *HttpProxyingIntegrationSuite) SetupTest() {
	s.authorizer.expectedToken = testToken
	s.authorizer.returnErr = nil
}

func (s *HttpProxyingIntegrationSuite) reverseProxyClient(listener net.Listener) *http.Client {
	proxyTlsClientConfig := s.tlsConfigForTest()
	proxyTlsClientConfig.InsecureSkipVerify = true // the proxy can be dialed over many different hostnames
	// TODO(mwitkow): Add http2.ConfigureTransport.

	dialer := &net.Dialer{
		Timeout:   15 * time.Second,
		KeepAlive: 15 * time.Second,
	}
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.DialContext(ctx, "tcp", listener.Addr().String())
			},
			TLSClientConfig: proxyTlsClientConfig,
		},
	}
}

func (s *HttpProxyingIntegrationSuite) forwardProxyClient(listener net.Listener) *http.Client {
	client := s.reverseProxyClient(listener)
	// This will make all dials over the Proxy mechanism. For "http" schemes it will used FORWARD_PROXY semantics.
	// For "https" scheme it will use CONNECT proxy.
	(client.Transport).(*http.Transport).Proxy = func(req *http.Request) (*url.URL, error) {
		if listener == s.proxyListenerPlain {
			return urlMustParse("http://address_overwritten_in_dialer_anyway"), nil
		}
		return nil, errors.New("Golang proxy logic cannot use HTTPS connections to proxy. Saad.")
	}
	return client
}

func (s *HttpProxyingIntegrationSuite) buildBackends() {
	s.localBackends = make(map[string]*localBackends)
	nonSecure := &localBackends{}
	for i := 0; i < nonSecureBackendCount; i++ {
		nonSecure.addServer(s.T(), nil /* notls */)
	}
	nonSecure.setResolvableCount(100)
	s.localBackends["_http._tcp.nonsecure.backends.test.local"] = nonSecure

	secure := &localBackends{}
	http2ServerTlsConfig, err := connhelpers.TlsConfigWithHttp2Enabled(s.tlsConfigForTest())
	if err != nil {
		s.FailNow("cannot configure the tls config for http2")
	}
	for i := 0; i < secureBackendCount; i++ {
		secure.addServer(s.T(), http2ServerTlsConfig)
	}
	secure.setResolvableCount(100)
	s.localBackends["_https._tcp.secure.backends.test.local"] = secure

	killers := &localBackends{}
	for i := 0; i < 1; i++ {
		killers.addKillerServer(s.T(), nil)
	}
	killers.setResolvableCount(100)
	s.localBackends["_https._tcp.nonsecure.killerbackend.test.local"] = killers
}

func (s *HttpProxyingIntegrationSuite) assertSuccessfulPingback(req *http.Request, resp *http.Response, authValue string, err error) {
	require.NoError(s.T(), err, "no error on a call to a proxy addr")

	require.NotNil(s.T(), resp.Body, "body should not be empty")
	b, err := ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	require.NoError(s.T(), err, "no error on a read all body")
	s.Assert().Equal("TEST", string(b))

	assert.Empty(s.T(), resp.Header.Get("x-kedge-error"))
	require.Equal(s.T(), http.StatusAccepted, resp.StatusCode)
	assert.Equal(s.T(), "application/json", resp.Header.Get("content-type"))
	assert.Equal(s.T(), req.URL.Path, resp.Header.Get("x-test-req-url"), "path seen on backend must match requested path")
	assert.Equal(s.T(), req.URL.Host, resp.Header.Get("x-test-req-host"), "host seen on backend must match requested host")
	assert.Equal(s.T(), authValue, resp.Header.Get("x-test-auth-value"))
	assert.Empty(s.T(), resp.Header.Get("x-test-proxy-auth-value")) // Proxy value should be cut down.
}

func testRequest(url string, backendSecret string, proxySecret string) *http.Request {
	req := &http.Request{Method: "GET", URL: urlMustParse(url)}
	req.Header = http.Header{}
	if backendSecret != "" {
		req.Header.Set("Authorization", backendSecret)
	}
	if proxySecret != "" {
		req.Header.Set("Proxy-Authorization", proxySecret)
	}
	return req
}

func (s *HttpProxyingIntegrationSuite) TestSuccessOverForwardProxy_DialUsingAddresser() {
	// Pick a port of any non secure backend.
	addr := s.localBackends["_http._tcp.nonsecure.backends.test.local"].targets()[0].DialAddr
	port := addr[strings.LastIndex(addr, ":")+1:]
	req := testRequest(fmt.Sprintf("http://127-0-0-1.pods.test.local:%s/some/strict/path", port), "bearer abc1", testProxyAuthValue)
	resp, err := s.forwardProxyClient(s.proxyListenerPlain).Do(req)
	s.assertSuccessfulPingback(req, resp, "bearer abc1", err)
	assert.Equal(s.T(), resp.Header.Get("x-test-req-proto"), "1.1", "non secure backends are dialed over HTTP/1.1")
}

func (s *HttpProxyingIntegrationSuite) TestSuccessOverReverseProxy_ToNonSecure_OverPlain() {
	req := testRequest("http://nonsecure.ext.example.com/some/strict/path", "bearer abc2", testProxyAuthValue)
	resp, err := s.reverseProxyClient(s.proxyListenerPlain).Do(req)
	s.assertSuccessfulPingback(req, resp, "bearer abc2", err)
	assert.Equal(s.T(), resp.Header.Get("x-test-req-proto"), "1.1", "non secure backends are dialed over HTTP/1.1")
}

func (s *HttpProxyingIntegrationSuite) TestSuccessOverReverseProxy_ToSecure_OverPlain() {
	req := testRequest("http://secure.ext.example.com/some/strict/path", "bearer abc3", testProxyAuthValue)
	resp, err := s.reverseProxyClient(s.proxyListenerPlain).Do(req)
	s.assertSuccessfulPingback(req, resp, "bearer abc3", err)
	assert.Equal(s.T(), resp.Header.Get("x-test-req-proto"), "2.0", "secure backends are dialed over HTTP2")
}

func (s *HttpProxyingIntegrationSuite) TestSuccessOverReverseProxy_ToNonSecure_OverTls() {
	req := testRequest("https://nonsecure.ext.example.com/some/strict/path", "bearer abc4", testProxyAuthValue)
	resp, err := s.reverseProxyClient(s.proxyListenerTls).Do(req)
	s.assertSuccessfulPingback(req, resp, "bearer abc4", err)
	assert.Equal(s.T(), resp.Header.Get("x-test-req-proto"), "1.1", "non secure backends are dialed over HTTP/1.1")
}

func (s *HttpProxyingIntegrationSuite) TestSuccessOverReverseProxy_ToSecure_OverTls() {
	req := testRequest("https://secure.ext.example.com/some/strict/path", "bearer abc5", testProxyAuthValue)
	resp, err := s.reverseProxyClient(s.proxyListenerTls).Do(req)
	s.assertSuccessfulPingback(req, resp, "bearer abc5", err)
	assert.Equal(s.T(), resp.Header.Get("x-test-req-proto"), "2.0", "secure backends are dialed over HTTP2")
}

func (s *HttpProxyingIntegrationSuite) TestSuccessOverForwardProxy_ToNonSecure_OverPlain() {
	req := testRequest("http://nonsecure.backends.test.local/some/strict/path", "bearer abc6", testProxyAuthValue)
	resp, err := s.forwardProxyClient(s.proxyListenerPlain).Do(req)
	s.assertSuccessfulPingback(req, resp, "bearer abc6", err)
	assert.Equal(s.T(), resp.Header.Get("x-test-req-proto"), "1.1", "non secure backends are dialed over HTTP/1.1")
}

func (s *HttpProxyingIntegrationSuite) TestSuccessOverForwardProxy_ToSecure_OverPlain() {
	req := testRequest("http://secure.backends.test.local/some/strict/path", "bearer abc7", testProxyAuthValue)
	resp, err := s.forwardProxyClient(s.proxyListenerPlain).Do(req)
	s.assertSuccessfulPingback(req, resp, "bearer abc7", err)
	assert.Equal(s.T(), resp.Header.Get("x-test-req-proto"), "2.0", "secure backends are dialed over HTTP2")
}

func (s *HttpProxyingIntegrationSuite) TestFailOverReverseProxy_ToForwardSecure_OverPlain() {
	req := testRequest("http://secure.backends.test.local/some/strict/path", "", testProxyAuthValue)
	resp, err := s.reverseProxyClient(s.proxyListenerPlain).Do(req)
	require.NoError(s.T(), err, "dialing should not fail")

	_, err = ioutil.ReadAll(resp.Body)
	s.Require().NoError(err, "no error on read all body")
	resp.Body.Close()

	assert.Equal(s.T(), http.StatusBadGateway, resp.StatusCode, "routing should fail")
	assert.Equal(s.T(), "unknown route to service", resp.Header.Get("x-kedge-error"), "routing error should be in the header")
}

func (s *HttpProxyingIntegrationSuite) TestFailOverForwardProxy_ToReverseNonSecure_OverPlain() {
	req := testRequest("http://nonsecure.ext.example.com/some/strict/path", "", testProxyAuthValue)
	resp, err := s.forwardProxyClient(s.proxyListenerPlain).Do(req)
	require.NoError(s.T(), err, "dialing should not fail")

	_, err = ioutil.ReadAll(resp.Body)
	s.Require().NoError(err, "no error on read all body")
	resp.Body.Close()

	assert.Equal(s.T(), http.StatusBadGateway, resp.StatusCode, "routing should fail")
	assert.Equal(s.T(), "unknown route to service", resp.Header.Get("x-kedge-error"), "routing error should be in the header")
}

func (s *HttpProxyingIntegrationSuite) TestFailOverForwardProxy_NoAuthForProxy() {
	req := testRequest("http://secure.backends.test.local/some/strict/path", "bearer abc7", "")
	resp, err := s.forwardProxyClient(s.proxyListenerPlain).Do(req)
	require.NoError(s.T(), err, "dialing should not fail")

	_, err = ioutil.ReadAll(resp.Body)
	s.Require().NoError(err, "no error on read all body")
	resp.Body.Close()

	assert.Equal(s.T(), http.StatusUnauthorized, resp.StatusCode, "routing should fail")
	assert.Equal(s.T(), "Unauthenticated. No Proxy-Authorization header.", resp.Header.Get("x-kedge-error"), "auth error should be in the header")
}

func (s *HttpProxyingIntegrationSuite) TestFailOverForwardProxy_AuthForProxyNotABearer() {
	req := testRequest("http://secure.backends.test.local/some/strict/path", "bearer abc7", "wrong_auth_token")
	resp, err := s.forwardProxyClient(s.proxyListenerPlain).Do(req)
	require.NoError(s.T(), err, "dialing should not fail")

	_, err = ioutil.ReadAll(resp.Body)
	s.Require().NoError(err, "no error on read all body")
	resp.Body.Close()

	assert.Equal(s.T(), http.StatusUnauthorized, resp.StatusCode, "routing should fail")
	assert.Equal(s.T(), "Unauthenticated. Proxy-Authorization header does not have Bearer format.", resp.Header.Get("x-kedge-error"), "auth error should be in the header")
}

func (s *HttpProxyingIntegrationSuite) TestFailOverForwardProxy_WrongAuthForProxy() {
	req := testRequest("http://secure.backends.test.local/some/strict/path", "bearer abc7", "Bearer wrong_auth_token")
	resp, err := s.forwardProxyClient(s.proxyListenerPlain).Do(req)
	require.NoError(s.T(), err, "dialing should not fail")

	_, err = ioutil.ReadAll(resp.Body)
	s.Require().NoError(err, "no error on read all body")
	resp.Body.Close()

	assert.Equal(s.T(), http.StatusUnauthorized, resp.StatusCode, "routing should fail")
	assert.Equal(s.T(), "Unauthenticated", resp.Header.Get("x-kedge-error"), "auth error should be in the header")
}

func (s *HttpProxyingIntegrationSuite) TestFailOverReverseProxy_NonSecureWithBadPath() {
	req := testRequest("http://nonsecure.ext.example.com/other_path", "", testProxyAuthValue)
	resp, err := s.reverseProxyClient(s.proxyListenerPlain).Do(req)
	require.NoError(s.T(), err, "dialing should not fail")

	_, err = ioutil.ReadAll(resp.Body)
	s.Require().NoError(err, "no error on read all body")
	resp.Body.Close()

	assert.Equal(s.T(), http.StatusBadGateway, resp.StatusCode, "routing should fail")
	assert.Equal(s.T(), "unknown route to service", resp.Header.Get("x-kedge-error"), "routing error should be in the header")
}

func (s *HttpProxyingIntegrationSuite) TestFailOverReverseProxy_BackendEOF() {
	req := testRequest("http://nonsecure.killerbackend.test.local/something", "", testProxyAuthValue)
	resp, err := s.reverseProxyClient(s.proxyListenerPlain).Do(req)
	require.NoError(s.T(), err, "dialing should not fail")

	_, err = ioutil.ReadAll(resp.Body)
	s.Require().NoError(err, "no error on read all body")
	resp.Body.Close()

	assert.Equal(s.T(), http.StatusBadGateway, resp.StatusCode, "EOF on backend")
	assert.Equal(s.T(), io.EOF.Error(), resp.Header.Get(header.ResponseKedgeError), "EOF error should be in the header")
}

func (s *HttpProxyingIntegrationSuite) TestLoadbalancingToSecureBackend() {
	backendResponse := make(map[string]int)
	for i := 0; i < secureBackendCount*10; i++ {
		req := testRequest("http://secure.backends.test.local/some/strict/path", fmt.Sprintf("bearer abc%d", i), testProxyAuthValue)
		resp, err := s.forwardProxyClient(s.proxyListenerPlain).Do(req)
		s.assertSuccessfulPingback(req, resp, fmt.Sprintf("bearer abc%d", i), err)
		addr := resp.Header.Get("x-test-backend-addr")
		if _, ok := backendResponse[addr]; ok {
			backendResponse[addr] += 1
		} else {
			backendResponse[addr] = 1
		}
	}
	assert.Len(s.T(), backendResponse, secureBackendCount, "requests should hit all backends")
	for addr, value := range backendResponse {
		assert.Equal(s.T(), 10, value, "backend %v should have received the same amount of requests", addr)
	}
}

func (s *HttpProxyingIntegrationSuite) TestLoadbalancingToNonSecureBackend() {
	backendResponse := make(map[string]int)
	for i := 0; i < nonSecureBackendCount*10; i++ {
		req := testRequest("http://nonsecure.ext.example.com/some/strict/path", fmt.Sprintf("bearer abc%d", i), testProxyAuthValue)
		resp, err := s.reverseProxyClient(s.proxyListenerPlain).Do(req)
		s.assertSuccessfulPingback(req, resp, fmt.Sprintf("bearer abc%d", i), err)
		addr := resp.Header.Get("x-test-backend-addr")
		if _, ok := backendResponse[addr]; ok {
			backendResponse[addr] += 1
		} else {
			backendResponse[addr] = 1
		}
	}
	assert.Len(s.T(), backendResponse, nonSecureBackendCount, "requests should hit all backends")
	for addr, value := range backendResponse {
		assert.Equal(s.T(), 10, value, "backend %v should have received the same amount of requests", addr)
	}
}

func (s *HttpProxyingIntegrationSuite) TestCallOverClient() {
	req := testRequest("http://nonsecure.ext.example.com/some/strict/path", "bearer abc8", testProxyAuthValue)
	resp, err := s.kedgeClient.Do(req)
	s.assertSuccessfulPingback(req, resp, "bearer abc8", err)
}

func (s *HttpProxyingIntegrationSuite) TestCallOverClient_WithPort_MatchWithGenericRoute() {
	// No matter what port we put here, it should pass, since there is port matching on this route.
	req := testRequest("http://nonsecure.ext.example.com:8435/some/strict/path", "bearer abc8", testProxyAuthValue)
	resp, err := s.kedgeClient.Do(req)
	s.assertSuccessfulPingback(req, resp, "bearer abc8", err)
}

func (s *HttpProxyingIntegrationSuite) TestCallOverClient_WithPort_SpecialRoute() {
	req := testRequest("http://nonsecure.ext.withport.example.com:81/some/strict/path", "bearer abc8", testProxyAuthValue)
	resp, err := s.kedgeClient.Do(req)
	s.assertSuccessfulPingback(req, resp, "bearer abc8", err)
}

func (s *HttpProxyingIntegrationSuite) TestCallOverClient_WithoutPort_SpecialRoute() {
	req := testRequest("http://nonsecure.ext.withoutport.example.com/some/strict/path", "bearer abc8", testProxyAuthValue)
	resp, err := s.kedgeClient.Do(req)
	s.assertSuccessfulPingback(req, resp, "bearer abc8", err)
}

func (s *HttpProxyingIntegrationSuite) TestCallOverClient_WithoutPort2_SpecialRoute() {
	req := testRequest("http://nonsecure.ext.withoutport.example.com:80/some/strict/path", "bearer abc8", testProxyAuthValue)
	resp, err := s.kedgeClient.Do(req)
	s.assertSuccessfulPingback(req, resp, "bearer abc8", err)
}

func (s *HttpProxyingIntegrationSuite) TestCallOverClient_WrongPort_SpecialRoute() {
	req := testRequest("http://nonsecure.ext.withoutport.example.com:82/some/strict/path", "bearer abc8", testProxyAuthValue)
	resp, err := s.kedgeClient.Do(req)
	require.NoError(s.T(), err, "no error on a call to a proxy addr")

	require.NotNil(s.T(), resp.Body, "body should not be empty")
	b, err := ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	require.NoError(s.T(), err, "no error on a read all body")
	s.Assert().Equal("unknown route to service", string(b))
	s.Assert().Equal(http.StatusBadGateway, resp.StatusCode)
}

func (s *HttpProxyingIntegrationSuite) TearDownSuite() {
	// Restore old resolver.
	if s.originalSrvResolver != nil {
		srvresolver.ParentSrvResolver = s.originalSrvResolver
	}
	if s.originalAResolver != nil {
		adhoc.DefaultALookup = s.originalAResolver
	}

	time.Sleep(10 * time.Millisecond)
	if s.proxy != nil {
		s.proxyListenerTls.Close()
		s.proxyListenerPlain.Close()
		s.proxy.Close()
	}
	for _, be := range s.localBackends {
		be.Close()
	}
	s.backendPool.Close()
}

func (s *HttpProxyingIntegrationSuite) tlsConfigForTest() *tls.Config {
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

func urlMustParse(uStr string) *url.URL {
	u, err := url.Parse(uStr)
	if err != nil {
		panic(err)
	}
	return u
}
