package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/grpc-ecosystem/go-grpc-middleware"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	"github.com/grpc-ecosystem/go-grpc-middleware/tags"
	"github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/improbable-eng/go-httpwares/logging/logrus"
	"github.com/improbable-eng/go-httpwares/tags"
	"github.com/improbable-eng/go-httpwares/tracing/debug"
	"github.com/improbable-eng/kedge/pkg/map"
	"github.com/improbable-eng/kedge/pkg/reporter"
	"github.com/improbable-eng/kedge/pkg/sharedflags"
	"github.com/improbable-eng/kedge/pkg/tls"
	"github.com/improbable-eng/kedge/pkg/winch"
	"github.com/improbable-eng/kedge/pkg/winch/grpc"
	"github.com/improbable-eng/kedge/pkg/winch/http"
	pb_config "github.com/improbable-eng/kedge/protogen/winch/config"
	"github.com/mwitkow/go-conntrack"
	"github.com/mwitkow/go-flagz"
	"github.com/mwitkow/go-flagz/protobuf"
	"github.com/mwitkow/go-proto-validators"
	"github.com/mwitkow/grpc-proxy/proxy"
	"github.com/oklog/run"
	"github.com/pkg/errors"
	"github.com/pressly/chi"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/cors"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/trace"
	"google.golang.org/grpc"
)

var (
	flagHttpPort              = sharedflags.Set.Int("server_http_port", 8070, "TCP port to listen on for HTTP1.1/REST calls.")
	flagMetricsPath           = sharedflags.Set.String("server_metrics_path", "/metrics", "path on which to serve metrics")
	flagGrpcPort              = sharedflags.Set.Int("server_grpc_port", 8071, "TCP non-TLS port to listen on for insecure gRPC calls.")
	flagMonitoringAddressPort = sharedflags.Set.String("server_http_monitoring_address", "", "HTTP host:port to serve debug and metrics endpoints on. If empty, server_http_port will be used for that.")

	flagHttpMaxWriteTimeout = sharedflags.Set.Duration("server_http_max_write_timeout", 15*time.Second, "HTTP server config, max write duration.")
	flagHttpMaxReadTimeout  = sharedflags.Set.Duration("server_http_max_read_timeout", 15*time.Second, "HTTP server config, max read duration.")
	flagMapperConfig        = protoflagz.DynProto3(sharedflags.Set,
		"server_mapper_config",
		&pb_config.MapperConfig{},
		"Contents of the Winch Mapper configuration. Content or read from file if _path suffix.").
		WithFileFlag("").WithValidator(validateMapper)
	flagAuthConfig = protoflagz.DynProto3(sharedflags.Set,
		"server_auth_config",
		&pb_config.AuthConfig{},
		"Contents of the Winch Auth configuration. Content or read from file if _path suffix.").
		WithFileFlag("").WithValidator(validateMapper)
	flagCORSAllowedOrigins = sharedflags.Set.StringSlice("cors_allowed_origin", []string{}, "CORS allowed origins for proxy endpoint.")
	flagLogLevel           = sharedflags.Set.String("log_level", "info", "Log level")
	flagDebugMode          = sharedflags.Set.Bool("debug_mode", false, "If true debug mode is enabled. "+
		"This will force DEBUG log level on winch and will append header to the request signaling Kedge to Log to INFO all debug"+
		"level logs for this request, overriding the kedge log level setting.")
)

func validateMapper(msg proto.Message) error {
	if val, ok := msg.(validator.Validator); ok {
		if err := val.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func registerDebugEndpoints(reg *prometheus.Registry, mux *http.ServeMux) {
	mux.Handle(*flagMetricsPath, promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.Handle("/debug/flagz", http.HandlerFunc(flagz.NewStatusEndpoint(sharedflags.Set).ListFlags))
	mux.Handle("/debug/pprof/", http.HandlerFunc(pprof.Index))
	mux.Handle("/debug/pprof/cmdline", http.HandlerFunc(pprof.Cmdline))
	mux.Handle("/debug/pprof/profile", http.HandlerFunc(pprof.Profile))
	mux.Handle("/debug/pprof/symbol", http.HandlerFunc(pprof.Symbol))
	mux.Handle("/debug/pprof/trace", http.HandlerFunc(pprof.Trace))
	mux.Handle("/debug/traces", http.HandlerFunc(trace.Traces))
	mux.Handle("/debug/events", http.HandlerFunc(trace.Events))
	mux.Handle("/version", http.HandlerFunc(handleVersion))
}

func main() {
	if err := sharedflags.Set.Parse(os.Args); err != nil {
		log.WithError(err).Fatal("failed parsing flags")
	}
	if err := flagz.ReadFileFlags(sharedflags.Set); err != nil {
		log.WithError(err).Fatal("failed reading flagz from files")
	}

	lvl := log.DebugLevel
	if !*flagDebugMode {
		var err error
		lvl, err = log.ParseLevel(*flagLogLevel)
		if err != nil {
			log.WithError(err).Fatalf("Cannot parse log level: %s", *flagLogLevel)
		}
	}
	log.SetLevel(lvl)
	logEntry := log.NewEntry(log.StandardLogger())
	logEntry.Warn("Make sure you have enough file descriptors on your machine. Run ulimit -n <value> to set it for this terminal.")

	tlsConfig, err := kedge_tls.BuildClientTLSConfigFromFlags()
	if err != nil {
		log.WithError(err).Fatal("failed building TLS config from flags")
	}

	// TODO(bplotka): Add metrics.
	reg := prometheus.NewRegistry()

	httpPlainListener := buildListenerOrFail("http_plain", *flagHttpPort)

	pacHandle, err := winch.NewPacFromFlags(httpPlainListener.Addr().String())
	if err != nil {
		log.WithError(err).Fatalf("failed to init PAC handler")
	}
	mux := http.NewServeMux()
	mux.Handle("/wpad.dat", pacHandle)

	if *flagMonitoringAddressPort == "" {
		registerDebugEndpoints(reg, mux)
	}

	routes, err := winch.NewStaticRoutes(
		winch.NewAuthFactory(httpPlainListener.Addr().String(), mux),
		flagMapperConfig.Get().(*pb_config.MapperConfig),
		flagAuthConfig.Get().(*pb_config.AuthConfig),
	)
	if err != nil {
		log.WithError(err).Fatal("failed creating static routes")
	}

	var g run.Group
	{
		// Setup HTTP proxy (plain, no HTTP CONNECT yet).
		httpWinchHandler := http_winch.New(
			kedge_map.RouteMapper(routes.HTTP()),
			tlsConfig,
			logEntry,
			mux,
			*flagDebugMode,
		)

		proxyMux := cors.New(cors.Options{
			AllowedOrigins: *flagCORSAllowedOrigins,
		}).Handler(httpWinchHandler)

		srv := &http.Server{
			WriteTimeout: *flagHttpMaxWriteTimeout,
			ReadTimeout:  *flagHttpMaxReadTimeout,
			ErrorLog:     http_logrus.AsHttpLogger(logEntry),
			Handler: chi.Chain(
				http_ctxtags.Middleware("winch"),
				http_debug.Middleware(),
				http_logrus.Middleware(logEntry, http_logrus.WithLevels(allAsDebug)),
				reporter.Middleware(logEntry),
			).Handler(proxyMux),
		}
		g.Add(func() error {
			log.Infof("listening for HTTP Plain on: %v", httpPlainListener.Addr().String())
			err := srv.Serve(httpPlainListener)
			if err != nil {
				return errors.Wrap(err, "http_plain server error")
			}
			return nil
		}, func(error) {
			log.Infof("\nReceived an interrupt, stopping services...\n")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := srv.Shutdown(ctx); err != nil {
				log.WithError(err).Errorf("Failed to gracefully shutdown server.")
			}
			cancel()
			httpPlainListener.Close()
		})
	}
	// Setup optional external monitoring address.
	// This is useful when running winch as a sidecar. Thanks to this we can setup separate endpoint for monitoring.
	// It is not safe to expose normal HTTP and gRPC ports out of the local pod, because there is no auth in front of winch.
	if *flagMonitoringAddressPort != "" {
		mux := http.NewServeMux()
		registerDebugEndpoints(reg, mux)

		listener, err := net.Listen("tcp", *flagMonitoringAddressPort)
		if err != nil {
			log.WithError(err).Fatalf("failed listening for http monitor address on %v", *flagMonitoringAddressPort)
		}

		srv := &http.Server{Handler: mux}
		g.Add(func() error {
			log.Infof("listening for HTTP Monitoring endpoint on: %v", listener.Addr().String())
			err := srv.Serve(listener)
			if err != nil {
				return errors.Wrap(err, "HTTP monitoring server error")
			}
			return nil
		}, func(error) {
			log.Infof("\nReceived an interrupt, stopping services...\n")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := srv.Shutdown(ctx); err != nil {
				log.WithError(err).Errorf("Failed to gracefully shutdown server.")
			}
			cancel()
			listener.Close()
		})
	}
	{
		// Setup gRPC proxy (plain, no HTTP CONNECT yet).
		listener := buildListenerOrFail("grpc_plain", *flagGrpcPort)

		grpcWinchHandler := grpc_winch.New(kedge_map.RouteMapper(routes.GRPC()), tlsConfig, *flagDebugMode)
		srv := grpc.NewServer(
			grpc.CustomCodec(proxy.Codec()), // needed for winch to function.
			grpc.UnknownServiceHandler(proxy.TransparentHandler(grpcWinchHandler)),
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
		)

		g.Add(func() error {
			log.Infof("listening for gRPC Plain on: %v", listener.Addr().String())
			err := srv.Serve(listener)
			if err != nil {
				return errors.Wrap(err, "grpc_plain server error")
			}
			return nil
		}, func(error) {
			srv.GracefulStop()
			listener.Close()
		})
	}

	{
		cancel := make(chan struct{})
		g.Add(func() error {
			return interrupt(cancel)
		}, func(error) {
			log.Infof("\nReceived an interrupt, stopping services...\n")
			close(cancel)
		})
	}

	// Serve all.
	if err := g.Run(); err != nil {
		log.WithError(err).Fatal("winch failed")
	}
}

func buildListenerOrFail(name string, port int) net.Listener {
	addr := fmt.Sprintf("%s:%d", "127.0.0.1", port)
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

// NOTE: This might be too spammy. There is no good way to determine if the error is relevant to proxying itself or just it
// was backend/user error that's why all logs are DEBUG.
func allAsDebug(_ int) log.Level {
	return log.DebugLevel
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
