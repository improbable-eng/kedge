package main

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"strings"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	"github.com/grpc-ecosystem/go-grpc-middleware/tags"
	"github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/mwitkow/go-conntrack"
	"github.com/mwitkow/go-conntrack/connhelpers"
	"github.com/mwitkow/go-flagz"
	"github.com/mwitkow/go-httpwares/logging/logrus"
	"github.com/mwitkow/go-httpwares/tags"
	"github.com/mwitkow/go-httpwares/tracing/debug"
	"github.com/mwitkow/grpc-proxy/proxy"
	http_director "github.com/mwitkow/kedge/http/director"
	"github.com/mwitkow/kedge/lib/http/ctxtags"
	"github.com/mwitkow/kedge/lib/sharedflags"
	"github.com/pressly/chi"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/trace" // so /debug/request gets registered.
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

var (
	flagBindAddr                = sharedflags.Set.String("server_bind_address", "0.0.0.0", "address to bind the server to")
	flagGrpcTlsPort             = sharedflags.Set.Int("server_grpc_tls_port", 8444, "TCP TLS port to listen on for secure gRPC calls. If 0, no gRPC-TLS will be open.")
	flagHttpTlsPort             = sharedflags.Set.Int("server_http_tls_port", 8443, "TCP port to listen on for HTTPS. If gRPC call will hit it will bounce to gRPC handler. If 0, no TLS will be open.")
	flagHttpPort                = sharedflags.Set.Int("server_http_port", 8080, "TCP port to listen on for HTTP1.1/REST calls for debug endpoints like metrics, flagz page or optional pprof (insecure, but private only IP are allowed). If 0, no insecure HTTP will be open.")
	flagDebugHttpAllowedIPs     = sharedflags.Set.StringSlice("server_debug_http_allowed_ips", nil, "Comma delimited list of IPs that are allowed to access debug endpoints in addtion to private IPs.")
	flagDebugHttpPublicProxyIPs = sharedflags.Set.StringSlice("server_debug_http_public_proxy_ips", nil, "Comma delimited list of IPs that are public proxies (e.g load balancers) which are before kedge. "+
		"This allows to properly assume remote address to enable 'server_debug_http_allowed_ips' logic")

	flagHttpMaxWriteTimeout = sharedflags.Set.Duration("server_http_max_write_timeout", 10*time.Second, "HTTP server config, max write duration.")
	flagHttpMaxReadTimeout  = sharedflags.Set.Duration("server_http_max_read_timeout", 10*time.Second, "HTTP server config, max read duration.")
	flagGrpcWithTracing     = sharedflags.Set.Bool("server_tracing_grpc_enabled", true, "Whether enable gRPC tracing (could be expensive).")
)

func main() {
	if err := sharedflags.Set.Parse(os.Args); err != nil {
		log.Fatalf("failed parsing flags: %v", err)
	}
	if err := flagz.ReadFileFlags(sharedflags.Set); err != nil {
		log.Fatalf("failed reading flagz from files: %v", err)
	}
	log.SetOutput(os.Stdout)
	grpc.EnableTracing = *flagGrpcWithTracing
	logEntry := log.NewEntry(log.StandardLogger())
	grpc_logrus.ReplaceGrpcLogger(logEntry)
	tlsConfig := buildServerTlsOrFail()

	// GRPC kedge.
	grpcDirectorServer := grpc.NewServer(
		grpc.CustomCodec(proxy.Codec()), // needed for director to function.
		grpc.UnknownServiceHandler(proxy.TransparentHandler(grpcDirector)),
		grpc_middleware.WithUnaryServerChain(
			grpc_ctxtags.UnaryServerInterceptor(),
			grpc_logrus.UnaryServerInterceptor(logEntry),
			grpc_prometheus.UnaryServerInterceptor,
		),
		grpc_middleware.WithStreamServerChain(
			grpc_ctxtags.StreamServerInterceptor(),
			grpc_logrus.StreamServerInterceptor(logEntry),
			grpc_prometheus.StreamServerInterceptor,
		),
		grpc.Creds(credentials.NewTLS(tlsConfig)),
	)

	// HTTPS kedge chain.
	httpDirectorChain := chi.Chain(
		http_ctxtags.Middleware("proxy"),
		http_debug.Middleware(),
		http_logrus.Middleware(logEntry),
	)

	// HTTPs kedge chain.
	httpDebugChain := chi.Chain(
		http_ctxtags.Middleware("debug"),
		http_debug.Middleware(),
		http_logrus.Middleware(logEntry.WithField(ctxtags.TagsForScheme, "plain")),
	)
	// httpNonAuthDebugChain chain is shares the same base but will not include auth. It is for metrics and _healthz.
	httpNonAuthDebugChain := httpDebugChain

	authorizer, err := authorizerFromFlags(logEntry)
	if err != nil {
		log.WithError(err).Fatal("failed to create authorizer.")
	}

	if authorizer != nil {
		httpDirectorChain = append(httpDirectorChain, http_director.AuthMiddleware(authorizer))
		logEntry.Info("configured OIDC authorization for HTTPS proxy.")
	}

	// Bouncer.
	httpsBouncerServer := httpsBouncerServer(grpcDirectorServer, httpDirectorChain.Handler(httpDirector), logEntry)

	if authorizer != nil && *flagEnableOIDCAuthForDebugEnpoints {
		httpDebugChain = append(httpDebugChain, http_director.AuthMiddleware(authorizer))
		logEntry.Info("configured OIDC authorization for HTTP debug server.")
	}

	// Debug.
	httpDebugServer, err := debugServer(logEntry, httpDebugChain, httpNonAuthDebugChain)
	if err != nil {
		log.WithError(err).Fatal("failed to create debug Server.")
	}

	errChan := make(chan error)
	var grpcTlsListener net.Listener
	var httpPlainListener net.Listener
	var httpTlsListener net.Listener
	if *flagGrpcTlsPort != 0 {
		grpcTlsListener = buildListenerOrFail("grpc_tls", *flagGrpcTlsPort)
	}
	if *flagHttpPort != 0 {
		httpPlainListener = buildListenerOrFail("http_plain", *flagHttpPort)
	}
	if *flagHttpTlsPort != 0 {
		httpTlsListener = buildListenerOrFail("http_tls", *flagHttpTlsPort)
		http2TlsConfig, err := connhelpers.TlsConfigWithHttp2Enabled(tlsConfig)
		if err != nil {
			log.Fatalf("failed setting up HTTP2 TLS config: %v", err)
		}
		httpTlsListener = tls.NewListener(httpTlsListener, http2TlsConfig)
	}

	if grpcTlsListener != nil {
		log.Infof("listening for gRPC TLS on: %v", grpcTlsListener.Addr().String())
		go func() {
			if err := grpcDirectorServer.Serve(grpcTlsListener); err != nil {
				errChan <- fmt.Errorf("grpc_tls server error: %v", err)
			}
		}()
	}
	if httpTlsListener != nil {
		log.Infof("listening for HTTP TLS on: %v", httpTlsListener.Addr().String())
		go func() {
			if err := httpsBouncerServer.Serve(httpTlsListener); err != nil {
				errChan <- fmt.Errorf("http_tls server error: %v", err)
			}
		}()
	}
	if httpPlainListener != nil {
		log.Infof("listening for HTTP Plain on: %v", httpPlainListener.Addr().String())
		go func() {
			if err := httpDebugServer.Serve(httpPlainListener); err != nil {
				errChan <- fmt.Errorf("http_plain server error: %v", err)
			}
		}()
	}

	err = <-errChan // this waits for some server breaking
	log.WithError(err).Fatalf("Fail")
}

// httpsBouncerHandler decides what kind of requests it is and redirects to GRPC if needed.
func httpsBouncerServer(grpcHandler *grpc.Server, httpHandler http.Handler, logEntry *log.Entry) *http.Server {
	httpBouncerHandler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/_healthz" {
			healthEndpoint(w, req)
			return
		}
		if strings.HasPrefix(req.Header.Get("content-type"), "application/grpc") {
			grpcHandler.ServeHTTP(w, req)
			return
		}
		httpHandler.ServeHTTP(w, req)
	}).ServeHTTP

	return &http.Server{
		WriteTimeout: *flagHttpMaxWriteTimeout,
		ReadTimeout:  *flagHttpMaxReadTimeout,
		ErrorLog:     http_logrus.AsHttpLogger(logEntry.WithField(ctxtags.TagsForScheme, "tls")),
		Handler:      http.HandlerFunc(httpBouncerHandler),
	}
}

func debugServer(logEntry *log.Entry, middlewares chi.Middlewares, noAuthMiddlewares chi.Middlewares) (*http.Server, error) {
	m := chi.NewMux()
	m.Handle("/_healthz", noAuthMiddlewares.HandlerFunc(healthEndpoint))
	m.Handle("/debug/metrics", noAuthMiddlewares.Handler(prometheus.UninstrumentedHandler()))

	m.Handle("/_version", middlewares.HandlerFunc(versionEndpoint))
	m.Handle("/debug/flagz", middlewares.HandlerFunc(flagz.NewStatusEndpoint(sharedflags.Set).ListFlags))

	m.Handle("/debug/pprof/", middlewares.HandlerFunc(pprof.Index))
	m.Handle("/debug/pprof/cmdline", middlewares.HandlerFunc(pprof.Cmdline))
	m.Handle("/debug/pprof/profile", middlewares.HandlerFunc(pprof.Profile))
	m.Handle("/debug/pprof/symbol", middlewares.HandlerFunc(pprof.Symbol))
	m.Handle("/debug/pprof/trace", middlewares.HandlerFunc(pprof.Trace))
	m.Handle("/debug/traces", middlewares.HandlerFunc(trace.Traces))
	m.Handle("/debug/events", middlewares.HandlerFunc(trace.Events))

	return &http.Server{
		WriteTimeout: *flagHttpMaxWriteTimeout,
		ReadTimeout:  *flagHttpMaxReadTimeout,
		ErrorLog:     http_logrus.AsHttpLogger(logEntry.WithField(ctxtags.TagsForScheme, "tls")),
		Handler:      m,
	}, nil
}

func buildListenerOrFail(name string, port int) net.Listener {
	addr := fmt.Sprintf("%s:%d", *flagBindAddr, port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("failed listening for '%v' on %v: %v", name, port, err)
	}
	return conntrack.NewListener(listener,
		conntrack.TrackWithName(name),
		conntrack.TrackWithTcpKeepAlive(20*time.Second),
		conntrack.TrackWithTracing(),
	)
}

func healthEndpoint(resp http.ResponseWriter, req *http.Request) {
	resp.Header().Set("content-type", "text/plain")
	resp.WriteHeader(http.StatusOK)
	fmt.Fprintf(resp, "kedge isok")
}
