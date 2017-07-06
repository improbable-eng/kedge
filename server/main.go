package main

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	_ "net/http/pprof"
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
	"github.com/mwitkow/kedge/lib/sharedflags"
	"github.com/pressly/chi"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	_ "golang.org/x/net/trace" // so /debug/requst gets registered.
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

var (
	flagBindAddr    = sharedflags.Set.String("server_bind_address", "0.0.0.0", "address to bind the server to")
	flagGrpcTlsPort = sharedflags.Set.Int("server_grpc_tls_port", 8444, "TCP TLS port to listen on for secure gRPC calls. If 0, no gRPC-TLS will be open.")
	flagHttpPort    = sharedflags.Set.Int("server_http_port", 8080, "TCP port to listen on for HTTP1.1/REST calls (insecure, debug). If 0, no insecure HTTP will be open.")
	flagHttpTlsPort = sharedflags.Set.Int("server_http_tls_port", 8443, "TCP port to listen on for HTTPS. If 0, no TLS will be open.")

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

	grpcServer := grpc.NewServer(
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
	//grpc_prometheus.Register(grpcServer)
	registerDebugHandlers()

	secureDirectorChain := chi.Chain(
		http_ctxtags.Middleware("proxy"),
		http_debug.Middleware(),
		http_logrus.Middleware(logEntry)).Handler(httpDirector)
	secureHandler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/_healthz" {
			healthEndpoint(w, req)
			return
		}
		if strings.HasPrefix(req.Header.Get("content-type"), "application/grpc") {
			grpcServer.ServeHTTP(w, req)
			return
		}
		secureDirectorChain.ServeHTTP(w, req)
	}).ServeHTTP

	proxyServer := &http.Server{
		WriteTimeout: *flagHttpMaxWriteTimeout,
		ReadTimeout:  *flagHttpMaxReadTimeout,
		ErrorLog:     http_logrus.AsHttpLogger(logEntry.WithField("http.port", "tls")),
		Handler:      http.HandlerFunc(secureHandler),
	}

	debugServer := &http.Server{
		WriteTimeout: *flagHttpMaxWriteTimeout,
		ReadTimeout:  *flagHttpMaxReadTimeout,
		ErrorLog:     http_logrus.AsHttpLogger(logEntry.WithField("http.port", "tls")),
		Handler: chi.Chain(
			http_ctxtags.Middleware("debug"),
			http_debug.Middleware(),
			http_logrus.Middleware(logEntry.WithField("http.port", "plain")),
		).Handler(http.DefaultServeMux),
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
			if err := grpcServer.Serve(grpcTlsListener); err != nil {
				errChan <- fmt.Errorf("grpc_tls server error: %v", err)
			}
		}()
	}
	if httpTlsListener != nil {
		log.Infof("listening for HTTP TLS on: %v", httpTlsListener.Addr().String())
		go func() {
			if err := proxyServer.Serve(httpTlsListener); err != nil {
				errChan <- fmt.Errorf("http_tls server error: %v", err)
			}
		}()
	}
	if httpPlainListener != nil {
		log.Infof("listening for HTTP Plain on: %v", httpPlainListener.Addr().String())
		go func() {
			if err := debugServer.Serve(httpPlainListener); err != nil {
				errChan <- fmt.Errorf("http_plain server error: %v", err)
			}
		}()
	}

	err := <-errChan // this waits for some server breaking
	log.Fatalf("Error: %v", err)
}

func registerDebugHandlers() {
	// TODO(mwitkow): Add middleware for making these only visible to private IPs.
	http.Handle("/_healthz", http.HandlerFunc(healthEndpoint))
	http.Handle("/debug/metrics", prometheus.UninstrumentedHandler())
	http.Handle("/debug/flagz", http.HandlerFunc(flagz.NewStatusEndpoint(sharedflags.Set).ListFlags))
	//http.Handle("/debug/pprof/", http.HandlerFunc(pprof.Index))
	//http.Handle("/debug/pprof/cmdline", http.HandlerFunc(pprof.Cmdline))
	//http.Handle("/debug/pprof/profile", http.HandlerFunc(pprof.Profile))
	//http.Handle("/debug/pprof/symbol", http.HandlerFunc(pprof.Symbol))
	//http.Handle("/debug/pprof/trace", http.HandlerFunc(pprof.Trace))
	//http.Handle("/debug/events", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
	//	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	//	trace.Render(w, req /*sensitive*/, true)
	//}))
	//http.Handle("/debug/events", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
	//	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	//	trace.RenderEvents(w, req /*sensitive*/, true)
	//}))
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
