package e2e

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/util/metautils"
	"github.com/improbable-eng/kedge/protogen/e2e"
	"github.com/oklog/run"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	httpWinchPort = "18070"
	grpcWinchPort = "18071"

	httpKedgePort    = "18170"
	httpTLSKedgePort = "18171"
	grpcTLSKedgePort = "18172"

	httpTestEndpointPort = "18270"
	grpcTestEndpointPort = "18271"

	miscDir = "../../misc/"

	endpointDNS       = "test_endpoint.localhost.internal.example.com"
	noAuthEndpointDNS = "no_auth.test_endpoint.localhost.internal.example.com"
)

type config struct {
	winch        bool
	kedge        bool
	testEndpoint bool
}

// NOTE: It is important to `make build` before using this function to compile latest changes.
// spinup runs winch, kedge and test server, connected to each other.
func spinup(t testing.TB, ctx context.Context, cfg config) (chan error, error) {
	var commands []*exec.Cmd

	if cfg.winch {
		commands = append(commands, exec.Command("winch",
			"--server_http_port", httpWinchPort,
			"--server_grpc_port", grpcWinchPort,
			"--server_mapper_config_path", miscDir+"winch_mapper.json",
			"--server_auth_config_path", miscDir+"winch_auth.json",
			"--pac_redirect_sh_expressions", "*.*.internal.example.com",
			"--client_tls_cert_file", miscDir+"client.crt",
			"--client_tls_key_file", miscDir+"client.key",
			"--client_tls_root_ca_files", miscDir+"ca.crt",
			"--debug_mode", "true",
		))
	}

	if cfg.kedge {
		commands = append(commands, exec.Command("kedge",
			"--server_http_port", httpKedgePort,
			"--server_http_tls_port", httpTLSKedgePort,
			"--server_grpc_tls_port", grpcTLSKedgePort,
			"--server_tls_cert_file", miscDir+"localhost.crt",
			"--server_tls_key_file", miscDir+"localhost.key",
			"--server_tls_client_ca_files", miscDir+"ca.crt",
			"--server_tls_client_cert_required", "true",
			"--kedge_config_director_config_path", miscDir+"director.json",
			"--kedge_config_backendpool_config_path", miscDir+"backendpool.json",
			"--log_level", "debug",
		))
	}

	var g run.Group

	if cfg.testEndpoint {
		// Tokens are specified in misc/winch_auth.json
		err := startTestEndpoints(&g, "secret-token2", "secret-token")
		if err != nil {
			return nil, err
		}
	}

	// Interrupt go routine.
	{
		ctx, cancel := context.WithCancel(ctx)
		g.Add(func() error {
			<-ctx.Done()

			// This go routine will return only when:
			// 1) Any other process from group exited unexpectedly
			// 2) Global context will be cancelled.
			return nil
		}, func(error) {
			cancel()
		})
	}

	// Run go routine for each command.
	for _, c := range commands {
		var stderr, stdout bytes.Buffer
		c.Stderr = &stderr
		c.Stdout = &stdout

		err := c.Start()
		if err != nil {
			// Let already started commands finish.
			go g.Run()
			return nil, errors.Wrap(err, "failed to start")
		}

		cmd := c
		g.Add(func() error {
			err := cmd.Wait()

			if stderr.Len() > 0 {
				t.Logf("%s STDERR\n %s", cmd.Path, stderr.String())
			}
			if stdout.Len() > 0 {
				t.Logf("%s STDOUT\n %s", cmd.Path, stdout.String())
			}

			return err
		}, func(error) {
			cmd.Process.Signal(syscall.SIGTERM)
		})
	}

	var exit = make(chan error, 1)
	go func(g run.Group) {
		exit <- g.Run()
		close(exit)
	}(g)

	return exit, nil
}

func expectedResponse(name string) string {
	return fmt.Sprintf("Hello %v!", name)
}

func startTestEndpoints(g *run.Group, requiredHTTPToken string, requiredGRPCToken string) error {
	log := logrus.New().WithField("bin", "testEndpoints")

	s := &greetingsServer{
		requiredHTTPToken: requiredHTTPToken,
		requiredGRPCToken: requiredGRPCToken,
	}
	{
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%s", httpTestEndpointPort))
		if err != nil {
			return err
		}

		httpServer := &http.Server{
			Handler: http.HandlerFunc(s.sayHelloHandler),
		}

		g.Add(func() error {
			log.Infof("listening for HTTP Plain on: %v", listener.Addr().String())
			err := httpServer.Serve(listener)
			if err != nil {
				return errors.Wrap(err, "http_plain server error")
			}
			return nil
		}, func(error) {
			log.Infof("\nReceived an interrupt, stopping services...\n")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := httpServer.Shutdown(ctx)
			if err != nil {
				log.WithError(err).Errorf("Failed to gracefully shutdown server.")
			}
			cancel()
			listener.Close()
		})
	}
	{
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%v", grpcTestEndpointPort))
		if err != nil {
			return err
		}

		grpcServer := grpc.NewServer()
		e2e_helloworld.RegisterGreeterServer(grpcServer, s)

		g.Add(func() error {
			log.Infof("listening for gRPC Plain on: %v", listener.Addr().String())
			err := grpcServer.Serve(listener)
			if err != nil {
				return errors.Wrap(err, "grpc_plain server error")
			}
			return nil
		}, func(error) {
			grpcServer.GracefulStop()
			listener.Close()
		})
	}

	return nil
}

type greetingsServer struct {
	requiredGRPCToken string
	requiredHTTPToken string
}

func (s *greetingsServer) SayHello(ctx context.Context, r *e2e_helloworld.HelloRequest) (*e2e_helloworld.HelloReply, error) {
	token, err := authFromMD(ctx)
	if err != nil {
		return nil, err
	}

	if token != s.requiredGRPCToken {
		return nil, status.Error(codes.Unauthenticated, "wrong token")
	}

	return &e2e_helloworld.HelloReply{
		Message: expectedResponse(r.Name),
	}, nil
}

func authFromMD(ctx context.Context) (string, error) {
	val := metautils.ExtractIncoming(ctx).Get("authorization")
	if val == "" {
		return "", status.Errorf(codes.Unauthenticated, "Request unauthenticated. No authorization header")

	}
	splits := strings.SplitN(val, " ", 2)
	if len(splits) != 2 {
		return "", status.Errorf(codes.Unauthenticated, "Bad authorization string")
	}
	if strings.ToLower(splits[0]) != "bearer" {
		return "", status.Errorf(codes.Unauthenticated, "Request unauthenticated. Not bearer type")
	}
	return splits[1], nil
}

func (s *greetingsServer) sayHelloHandler(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("authorization")
	if token == "" {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("No authorization header"))
		return
	}

	splits := strings.SplitN(token, " ", 2)
	if len(splits) != 2 {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("Bad authorization string"))
		return
	}
	if strings.ToLower(splits[0]) != "bearer" {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("Bad authorization string. No bearer"))
		return
	}

	if splits[1] != s.requiredHTTPToken {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("wrong token"))
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(expectedResponse(r.URL.Query().Get("name"))))
}
