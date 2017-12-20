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
	"github.com/oklog/oklog/pkg/group"
	"github.com/pkg/errors"
	"github.com/pressly/chi"
	"github.com/rs/cors"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/trace"
	"google.golang.org/grpc"
)

var (
	flagHttpPort = sharedflags.Set.Int("server_http_port", 8070, "TCP port to listen on for HTTP1.1/REST calls.")
	flagGrpcPort = sharedflags.Set.Int("server_grpc_port", 8071, "TCP non-TLS port to listen on for insecure gRPC calls.")

	flagHttpMaxWriteTimeout = sharedflags.Set.Duration("server_http_max_write_timeout", 15*time.Second, "HTTP server config, max write duration.")
	flagHttpMaxReadTimeout  = sharedflags.Set.Duration("server_http_max_read_timeout", 15*time.Second, "HTTP server config, max read duration.")
	flagMapperConfig        = protoflagz.DynProto3(sharedflags.Set,
		"server_mapper_config",
		&pb_config.MapperConfig{},
		"Contents of the Winch Mapper configuration. Content or read from file if _path suffix.").
		WithFileFlag("../../misc/winch_mapper.json").WithValidator(validateMapper)
	flagAuthConfig = protoflagz.DynProto3(sharedflags.Set,
		"server_auth_config",
		&pb_config.AuthConfig{},
		"Contents of the Winch Auth configuration. Content or read from file if _path suffix.").
		WithFileFlag("../../misc/winch_auth.json").WithValidator(validateMapper)
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

func main() {
	if err := sharedflags.Set.Parse(os.Args); err != nil {
		log.WithError(err).Fatal("failed parsing flags")
	}
	if err := flagz.ReadFileFlags(sharedflags.Set); err != nil {
		log.WithError(err).Fatal("failed reading flagz from files")
	}
	tlsConfig, err := kedge_tls.BuildClientTLSConfigFromFlags()
	if err != nil {
		log.WithError(err).Fatal("failed building TLS config from flags")
	}

	lvl := log.DebugLevel
	if !*flagDebugMode {
		lvl, err = log.ParseLevel(*flagLogLevel)
		if err != nil {
			log.WithError(err).Fatalf("Cannot parse log level: %s", *flagLogLevel)
		}
	}
	log.SetLevel(lvl)
	logEntry := log.NewEntry(log.StandardLogger())
	logEntry.Warn("Make sure you have enough file descriptors on your machine. Run ulimit -n <value> to set it for this terminal.")

	var httpPlainListener net.Listener
	httpPlainListener = buildListenerOrFail("http_plain", *flagHttpPort)
	log.Infof("listening for HTTP Plain on: %v", httpPlainListener.Addr().String())

	var grpcPlainListener net.Listener
	grpcPlainListener = buildListenerOrFail("grpc_plain", *flagGrpcPort)
	log.Infof("listening for gRPC Plain on: %v", grpcPlainListener.Addr().String())

	mux := http.NewServeMux()
	mux.Handle("/debug/flagz", http.HandlerFunc(flagz.NewStatusEndpoint(sharedflags.Set).ListFlags))
	mux.Handle("/debug/pprof/", http.HandlerFunc(pprof.Index))
	mux.Handle("/debug/pprof/cmdline", http.HandlerFunc(pprof.Cmdline))
	mux.Handle("/debug/pprof/profile", http.HandlerFunc(pprof.Profile))
	mux.Handle("/debug/pprof/symbol", http.HandlerFunc(pprof.Symbol))
	mux.Handle("/debug/pprof/trace", http.HandlerFunc(pprof.Trace))
	mux.Handle("/debug/traces", http.HandlerFunc(trace.Traces))
	mux.Handle("/debug/events", http.HandlerFunc(trace.Events))
	mux.Handle("/version", http.HandlerFunc(handleVersion))
	pacHandle, err := winch.NewPacFromFlags(httpPlainListener.Addr().String())
	if err != nil {
		log.WithError(err).Fatalf("failed to init PAC handler")
	}
	mux.Handle("/wpad.dat", pacHandle)

	routes, err := winch.NewStaticRoutes(
		winch.NewAuthFactory(httpPlainListener.Addr().String(), mux),
		flagMapperConfig.Get().(*pb_config.MapperConfig),
		flagAuthConfig.Get().(*pb_config.AuthConfig),
	)
	if err != nil {
		log.WithError(err).Fatal("failed reading flagz from files")
	}

	mapper := kedge_map.RouteMapper(routes.Get())
	httpWinchHandler := http_winch.New(
		mapper,
		tlsConfig,
		logEntry,
		mux,
		*flagDebugMode,
	)

	proxyMux := cors.New(cors.Options{
		AllowedOrigins: *flagCORSAllowedOrigins,
	}).Handler(httpWinchHandler)
	httpWinchServer := &http.Server{
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

	grpcWinchHandler := grpc_winch.New(mapper, tlsConfig, *flagDebugMode)

	grpcWinchServer := grpc.NewServer(
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

	var g group.Group
	{
		g.Add(func() error {
			err := httpWinchServer.Serve(httpPlainListener)
			if err != nil {
				return errors.Wrap(err, "http_plain server error")
			}
			return nil
		}, func(error) {
			log.Infof("\nReceived an interrupt, stopping services...\n")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := httpWinchServer.Shutdown(ctx)
			if err != nil {
				log.WithError(err).Errorf("Failed to gracefully shutdown server.")
			}
			cancel()
			httpPlainListener.Close()
		})
	}
	{
		g.Add(func() error {
			err := grpcWinchServer.Serve(grpcPlainListener)
			if err != nil {
				return errors.Wrap(err, "grpc_plain server error")
			}
			return nil
		}, func(error) {
			log.Infof("\nReceived an interrupt, stopping services...\n")

			// TODO(bplotka): Add timeout to these.
			grpcWinchServer.GracefulStop()
			grpcPlainListener.Close()
		})
	}
	{
		cancel := make(chan struct{})
		g.Add(func() error {
			return interrupt(cancel)
		}, func(error) {
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
