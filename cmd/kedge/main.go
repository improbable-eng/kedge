package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"strings"

	"github.com/grpc-ecosystem/go-grpc-middleware"
	"github.com/grpc-ecosystem/go-grpc-middleware/auth"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	"github.com/grpc-ecosystem/go-grpc-middleware/tags"
	"github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/improbable-eng/go-httpwares/logging/logrus"
	"github.com/improbable-eng/go-httpwares/metrics"
	"github.com/improbable-eng/go-httpwares/metrics/prometheus"
	"github.com/improbable-eng/go-httpwares/tags"
	"github.com/improbable-eng/go-httpwares/tracing/debug"
	"github.com/improbable-eng/kedge/pkg/discovery"
	"github.com/improbable-eng/kedge/pkg/http/ctxtags"
	"github.com/improbable-eng/kedge/pkg/http/header"
	grpc_director "github.com/improbable-eng/kedge/pkg/kedge/grpc/director"
	http_director "github.com/improbable-eng/kedge/pkg/kedge/http/director"
	"github.com/improbable-eng/kedge/pkg/logstash"
	"github.com/improbable-eng/kedge/pkg/reporter"
	"github.com/improbable-eng/kedge/pkg/sharedflags"
	pb_config "github.com/improbable-eng/kedge/protogen/kedge/config"
	"github.com/mwitkow/go-conntrack"
	"github.com/mwitkow/go-conntrack/connhelpers"
	"github.com/mwitkow/go-flagz"
	"github.com/mwitkow/grpc-proxy/proxy"
	"github.com/oklog/run"
	"github.com/pkg/errors"
	"github.com/pressly/chi"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

var (
	flagBindAddr    = sharedflags.Set.String("server_bind_address", "0.0.0.0", "address to bind the server to")
	flagMetricsPath = sharedflags.Set.String("server_metrics_path", "/debug/metrics", "path on which to serve metrics")
	flagGrpcTlsPort = sharedflags.Set.Int("server_grpc_tls_port", 8444, "TCP TLS port to listen on for secure gRPC calls. If 0, no gRPC-TLS will be open.")
	flagHttpTlsPort = sharedflags.Set.Int("server_http_tls_port", 8443, "TCP port to listen on for HTTPS. If gRPC call will hit it will bounce to gRPC handler. If 0, no TLS will be open.")
	flagHttpPort    = sharedflags.Set.Int("server_http_port", 8080, "TCP port to listen on for HTTP1.1/REST calls for debug endpoints like metrics, flagz page or optional pprof (insecure, but private only IP are allowed). If 0, no debug HTTP endpoint will be open.")

	flagHttpMaxWriteTimeout = sharedflags.Set.Duration("server_http_max_write_timeout", 10*time.Second, "HTTP server config, max write duration.")
	flagHttpMaxReadTimeout  = sharedflags.Set.Duration("server_http_max_read_timeout", 10*time.Second, "HTTP server config, max read duration.")
	flagGrpcWithTracing     = sharedflags.Set.Bool("server_tracing_grpc_enabled", true, "Whether enable gRPC tracing (could be expensive).")

	flagLogLevel        = sharedflags.Set.String("log_level", "info", "Log level")
	flagLogstashAddress = sharedflags.Set.String("logstash_hostport", "", "Host:port of logstash for remote logging. If empty remote logging is disabled.")

	flagLogTestBackendpoolResolution = sharedflags.Set.Bool("log_backend_resolution_on_addition", false, "With this option "+
		"kedge will always try to resolve and log (only) new backend entry. Useful for debugging backend routings.")

	flagDynamicRoutingDiscoveryEnabled = sharedflags.Set.Bool("kedge_dynamic_routings_enabled", false,
		"If enabled, kedge will watch on service changes (services which has particular label) and generates "+
			"director & backendpool routings. It will update them directly into into flagz value, so you can see the current routings anytime in debug/flagz")
)

func isGRPCReq(header http.Header) bool {
	// Do not treat "grpc-web" request as pure gRPC. It is pure HTTP/2 at this point.
	if strings.HasPrefix(header.Get("content-type"), "application/grpc-web") {
		return false
	}

	if strings.HasPrefix(header.Get("content-type"), "application/grpc") {
		return true
	}

	return false
}

func main() {
	if err := sharedflags.Set.Parse(os.Args); err != nil {
		log.WithError(err).Fatalf("failed parsing flags")
	}
	if err := flagz.ReadFileFlags(sharedflags.Set); err != nil {
		log.WithError(err).Fatalf("failed reading flagz from files")
	}

	lvl, err := log.ParseLevel(*flagLogLevel)
	if err != nil {
		log.WithError(err).Fatalf("Cannot parse log level: %s", *flagLogLevel)
	}
	log.SetLevel(lvl)

	if *flagLogstashAddress != "" {
		formatter, err := logstash.NewFormatter()
		if err != nil {
			log.WithError(err).Fatal("Failed to get hostname for logstash formatter")
		}

		hook, err := logstash.NewHook(*flagLogstashAddress, formatter)
		if err != nil {
			log.WithError(err).Fatal("Failed to create new logstash hook")
		}
		log.AddHook(hook)
	}

	grpc.EnableTracing = *flagGrpcWithTracing
	logEntry := log.NewEntry(log.StandardLogger())
	grpc_logrus.ReplaceGrpcLogger(logEntry)
	tlsConfig, err := buildTLSConfigFromFlags()
	if err != nil {
		log.Fatalf("failed building TLS config from flags: %v", err)
	}

	var g run.Group

	// Schedule Dynamic Routing Discovery if needed.
	if *flagDynamicRoutingDiscoveryEnabled {
		log.Info("Flag 'kedge_dynamic_routings_enabled' is true. Enabling dynamic routing with base configuration fetched from provided" +
			" directorConfig and backendpoolConfig.")
		routingDiscovery, err := discovery.NewFromFlags(
			log.StandardLogger(),
			flagConfigDirector.Get().(*pb_config.DirectorConfig),
			flagConfigBackendpool.Get().(*pb_config.BackendPoolConfig),
		)
		if err != nil {
			log.WithError(err).Fatal("Failed to create routingDiscovery")
		}

		ctx, cancel := context.WithCancel(context.Background())
		g.Add(func() error {
			err := routingDiscovery.DiscoverAndSetFlags(
				ctx,
				flagConfigDirector,
				flagConfigBackendpool,
			)
			if err != nil {
				return errors.Wrap(err, "dynamic routing discovery")
			}
			return nil
		}, func(error) {
			cancel()
		})
	}

	// authorizer decides how to auth the endpoint.
	authorizer, err := authorizerFromFlags(logEntry)
	if err != nil {
		log.WithError(err).Fatal("failed to create authorizer.")
	}

	var grpcServer *grpc.Server

	if *flagGrpcTlsPort != 0 {
		// Setup gRPC handling.
		grpcDirector := grpc_director.New(grpcBackendPool, grpcAddresser, grpcRouter)
		grpcUnaryInterceptors := []grpc.UnaryServerInterceptor{
			grpc_ctxtags.UnaryServerInterceptor(),
			grpc_logrus.UnaryServerInterceptor(logEntry),
			grpc_prometheus.UnaryServerInterceptor,
		}
		grpcStreamInterceptors := []grpc.StreamServerInterceptor{
			grpc_ctxtags.StreamServerInterceptor(),
			grpc_logrus.StreamServerInterceptor(logEntry),
			grpc_prometheus.StreamServerInterceptor,
		}
		if authorizer != nil {
			grpcAuth := grpc_director.NewGRPCAuthorizer(authorizer)
			grpcUnaryInterceptors = append(grpcUnaryInterceptors, grpc_auth.UnaryServerInterceptor(grpcAuth))
			grpcStreamInterceptors = append(grpcStreamInterceptors, grpc_auth.StreamServerInterceptor(grpcAuth))

			logEntry.Info("configured OIDC authorization for TLS gRPC.")
		}

		// GRPC kedge.
		grpcServer = grpc.NewServer(
			grpc.CustomCodec(proxy.Codec()), // needed for director to function.
			grpc.UnknownServiceHandler(proxy.TransparentHandler(grpcDirector)),
			grpc_middleware.WithUnaryServerChain(grpcUnaryInterceptors...),
			grpc_middleware.WithStreamServerChain(grpcStreamInterceptors...),
			grpc.Creds(credentials.NewTLS(tlsConfig)),
		)

		grpcTlsListener := buildListenerOrFail("grpc_tls", *flagGrpcTlsPort)
		g.Add(func() error {
			log.Infof("listening for gRPC TLS on: %v", grpcTlsListener.Addr().String())
			err := grpcServer.Serve(grpcTlsListener)
			if err != nil {
				return errors.Wrap(err, "grpc_tls")
			}
			return nil
		}, func(error) {
			grpcServer.GracefulStop()
			grpcTlsListener.Close()
		})
	}

	if *flagHttpTlsPort != 0 {
		// Setup HTTP handling (+ bouncer to gRPC if needed)
		httpDirector := http_director.New(httpBackendPool, httpRouter, httpAddresser, logEntry)

		// HTTPS proxy chain.
		httpDirectorChain := chi.Chain(
			http_ctxtags.Middleware("proxy", http_ctxtags.WithTagExtractor(kedgeRequestIDTagExtractor)), // Tags.
			http_debug.Middleware(),                                                                     // Traces.
			http_logrus.Middleware(logEntry, http_logrus.WithLevels(logAsDebug)),                        // Std Request/Response Logs.
			http_metrics.Middleware(http_prometheus.ServerMetrics()),                                    // Std Request/Response Metrics.
			reporter.Middleware(logEntry),                                                               // Kedge proxy metrics/logs
		)

		if authorizer != nil {
			httpDirectorChain = append(httpDirectorChain, http_director.AuthMiddleware(authorizer))
			logEntry.Info("configured OIDC authorization for HTTPS proxy.")
		}

		handler := httpDirectorChain.Handler(httpDirector)
		if grpcServer != nil {
			// Make HTTP handler bounce to gRPC if found proper content-type.
			handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if isGRPCReq(req.Header) {
					grpcServer.ServeHTTP(w, req)
					return
				}
				httpDirectorChain.Handler(httpDirector).ServeHTTP(w, req)
			})
		}

		httpsServer := &http.Server{
			WriteTimeout: *flagHttpMaxWriteTimeout,
			ReadTimeout:  *flagHttpMaxReadTimeout,
			ErrorLog:     http_logrus.AsHttpLogger(logEntry.WithField(ctxtags.TagForScheme, "tls")),
			Handler:      handler,
		}

		httpTlsListener := buildListenerOrFail("http_tls", *flagHttpTlsPort)
		http2TlsConfig, err := connhelpers.TlsConfigWithHttp2Enabled(tlsConfig)
		if err != nil {
			log.Fatalf("failed setting up HTTP2 TLS config: %v", err)
		}
		httpTlsListener = tls.NewListener(httpTlsListener, http2TlsConfig)

		g.Add(func() error {
			log.Infof("listening for HTTP TLS on: %v", httpTlsListener.Addr().String())
			err := httpsServer.Serve(httpTlsListener)
			if err != nil {
				return errors.Wrap(err, "http_tls")
			}
			return nil
		}, func(error) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			httpsServer.Shutdown(ctx)
			httpTlsListener.Close()
		})
	}

	if *flagHttpPort != 0 {
		// HTTP debug chain.
		httpDebugChain := chi.Chain(
			http_ctxtags.Middleware("debug"),
			http_debug.Middleware(),
		)

		if authorizer != nil && *flagEnableOIDCAuthForDebugEnpoints {
			httpDebugChain = append(httpDebugChain, http_director.AuthMiddleware(authorizer))
			logEntry.Info("configured OIDC authorization for HTTP debug server.")
		}
		// httpNonAuthDebugChain chain is shares the same base but will not include auth. It is for metrics and _healthz.
		httpNonAuthDebugChain := httpDebugChain

		// Debug.
		httpDebugServer, err := debugServer(logEntry, httpDebugChain, httpNonAuthDebugChain)
		if err != nil {
			log.WithError(err).Fatal("failed to create debug Server.")
		}
		httpPlainListener := buildListenerOrFail("http_plain", *flagHttpPort)

		g.Add(func() error {
			log.Infof("listening for HTTP plain on: %v", httpPlainListener.Addr().String())
			err := httpDebugServer.Serve(httpPlainListener)
			if err != nil {
				return errors.Wrap(err, "http_plain")
			}
			return nil
		}, func(error) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			httpDebugServer.Shutdown(ctx)
			httpPlainListener.Close()
		})
	}

	{
		cancel := make(chan struct{})
		g.Add(func() error {
			return interrupt(cancel)
		}, func(error) {
			log.Info("Shutting down servers gracefully.")
			close(cancel)
		})
	}
	// Serve all.
	if err := g.Run(); err != nil {
		log.WithError(err).Fatal("kedge failed")
	}
}

func debugServer(logEntry *log.Entry, middlewares chi.Middlewares, noAuthMiddlewares chi.Middlewares) (*http.Server, error) {
	m := chi.NewMux()
	m.Handle("/_healthz", noAuthMiddlewares.HandlerFunc(healthEndpoint))
	m.Handle(*flagMetricsPath, noAuthMiddlewares.Handler(promhttp.Handler()))

	m.Handle("/_version", middlewares.HandlerFunc(versionEndpoint))

	// NOTE: These can contain sensitive data like user headers.
	m.Handle("/debug/flagz", middlewares.HandlerFunc(flagz.NewStatusEndpoint(sharedflags.Set).ListFlags))

	m.Mount("/debug/pprof/", middlewares.HandlerFunc(pprof.Index))
	m.Handle("/debug/pprof/cmdline", middlewares.HandlerFunc(pprof.Cmdline))
	m.Handle("/debug/pprof/profile", middlewares.HandlerFunc(pprof.Profile))
	m.Handle("/debug/pprof/symbol", middlewares.HandlerFunc(pprof.Symbol))
	m.Handle("/debug/pprof/trace", middlewares.HandlerFunc(pprof.Trace))

	// Use Kedge auth.
	trace.AuthRequest = func(req *http.Request) (any, sensitive bool) { return true, true }
	m.Handle("/debug/traces", middlewares.HandlerFunc(trace.Traces))
	m.Handle("/debug/events", middlewares.HandlerFunc(trace.Events))

	return &http.Server{
		WriteTimeout: *flagHttpMaxWriteTimeout,
		ReadTimeout:  *flagHttpMaxReadTimeout,
		ErrorLog:     http_logrus.AsHttpLogger(logEntry.WithField(ctxtags.TagForScheme, "tls")),
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

// NOTE: There is no good way to determine if the error is relevant to proxying itself or just it
// was backend/user error here, so all of it just DEBUG.
func logAsDebug(_ int) log.Level {
	return log.DebugLevel
}

func kedgeRequestIDTagExtractor(req *http.Request) map[string]interface{} {
	tags := map[string]interface{}{}

	if requestID := req.Header.Get(header.RequestKedgeRequestID); requestID != "" {
		tags[ctxtags.TagRequestID] = requestID
	}

	return tags
}

func interrupt(cancel <-chan struct{}) error {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-c:
		return nil
	case <-cancel:
		return errors.New("canceled")
	}
}
