package backendpool

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware"
	"github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/mwitkow/go-conntrack"
	"github.com/mwitkow/grpc-proxy/proxy"
	pb "github.com/improbable-eng/kedge/_protogen/kedge/config/grpc/backends"
	"github.com/improbable-eng/kedge/lib/resolvers/k8s"
	"github.com/improbable-eng/kedge/lib/resolvers/srv"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/naming"
)

var (
	ParentDialFunc = (&net.Dialer{
		Timeout:   1 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext
)

type backend struct {
	mu     sync.RWMutex
	conn   *grpc.ClientConn
	config *pb.Backend
	closed bool

	target   string
	resolver naming.Resolver
}

func (b *backend) Conn() (*grpc.ClientConn, error) {
	// This needs to be lazy. Otherwise backends with zero resolutions will fail to be created and
	// recreated.
	b.mu.RLock()
	cc := b.conn
	b.mu.RUnlock()
	if cc != nil {
		return cc, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.conn != nil {
		return b.conn, nil
	}
	if b.closed {
		return nil, grpc.Errorf(codes.Internal, "backend already closed")
	}
	target, resolver, err := chooseNamingResolver(b.config)
	if err != nil {
		return nil, err
	}
	b.target = target
	b.resolver = resolver

	cc, err = buildClientConn(b.config, target, resolver)
	if err != nil {
		return nil, err
	}
	b.conn = cc
	return cc, nil
}

func (b *backend) Close() error {
	b.mu.Lock()
	b.closed = true
	defer b.mu.Unlock()
	if b.conn != nil {
		return b.conn.Close()
	}
	return nil
}

func newBackend(cnf *pb.Backend) (*backend, error) {
	b := &backend{
		config: cnf,
	}
	target, resolver, err := chooseNamingResolver(cnf)
	if err != nil {
		return nil, err
	}
	b.target = target
	b.resolver = resolver

	cc, err := buildClientConn(cnf, target, resolver)
	if err != nil && err.Error() == "grpc: there is no address available to dial" {
		return b, nil // make this lazy
	} else if err != nil {
		return nil, fmt.Errorf("backend '%v' dial error: %v", cnf.Name, err)
	}
	b.conn = cc
	return b, nil
}

func buildClientConn(cnf *pb.Backend, target string, resolver naming.Resolver) (*grpc.ClientConn, error) {
	opts := []grpc.DialOption{}
	opts = append(opts, chooseDialFuncOpt(cnf))
	opts = append(opts, chooseSecurityOpt(cnf))
	opts = append(opts, grpc.WithCodec(proxy.Codec())) // needed for the director to function at all.
	opts = append(opts, chooseInterceptors(cnf)...)
	opts = append(opts, grpc.WithBalancer(chooseBalancerPolicy(cnf, resolver)))
	return grpc.Dial(target, opts...)
}

func chooseDialFuncOpt(cnf *pb.Backend) grpc.DialOption {
	dialFunc := ParentDialFunc
	if !cnf.DisableConntracking {
		dialFunc = conntrack.NewDialContextFunc(
			conntrack.DialWithName("grpc_backend_"+cnf.Name),
			conntrack.DialWithDialContextFunc(dialFunc),
			conntrack.DialWithTracing(),
		)
	}
	return grpc.WithDialer(func(addr string, t time.Duration) (net.Conn, error) {
		ctx, _ := context.WithTimeout(context.Background(), t)
		return dialFunc(ctx, "tcp", addr)
	})
}

func chooseSecurityOpt(cnf *pb.Backend) grpc.DialOption {
	if sec := cnf.GetSecurity(); sec != nil {
		tlsConfig := &tls.Config{InsecureSkipVerify: true}
		if !sec.InsecureSkipVerify {
			// TODO(mwitkow): add configuration TlsConfig fetching by name here.
			panic("Not implemented") // Ugly but this matters.
		}
		return grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))
	} else {
		return grpc.WithInsecure()
	}
}

func chooseInterceptors(cnf *pb.Backend) []grpc.DialOption {
	unary := []grpc.UnaryClientInterceptor{}
	stream := []grpc.StreamClientInterceptor{}
	for _, i := range cnf.GetInterceptors() {
		if prom := i.GetPrometheus(); prom {
			unary = append(unary, grpc_prometheus.UnaryClientInterceptor)
			stream = append(stream, grpc_prometheus.StreamClientInterceptor)
		}
		// new interceptors are to be added here as else if statements.
	}
	return []grpc.DialOption{
		grpc.WithUnaryInterceptor(grpc_middleware.ChainUnaryClient(unary...)),
		grpc.WithStreamInterceptor(grpc_middleware.ChainStreamClient(stream...)),
	}
}

func chooseNamingResolver(cnf *pb.Backend) (string, naming.Resolver, error) {
	if s := cnf.GetSrv(); s != nil {
		return srvresolver.NewFromConfig(s)
	} else if k := cnf.GetK8S(); k != nil {
		return k8sresolver.NewFromConfig(k)
	}
	return "", nil, fmt.Errorf("unspecified naming resolver for %v", cnf.Name)
}

func chooseBalancerPolicy(cnf *pb.Backend, resolver naming.Resolver) grpc.Balancer {
	switch cnf.GetBalancer() {
	case pb.Balancer_ROUND_ROBIN:
		return grpc.RoundRobin(resolver)
	default:
		return grpc.RoundRobin(resolver)
	}
}

func (b *backend) LogTestResolution(logger logrus.FieldLogger) {
	logger = logger.WithField("target", b.target)

	// Mimick run-time resolution to check if the target makes sense.
	watcher, err := b.resolver.Resolve(b.target)
	if err != nil {
		logger.WithError(err).Error("Creating watcher failed.")
		return
	}

	var updates []*naming.Update
	ctx, cancel := context.WithCancel(context.TODO())
	go func() {
		for ctx.Err() == nil {
			u, err := watcher.Next()
			if err != nil {
				if ctx.Err() != nil {
					cancel()
					return
				}
				logger.WithError(err).Error("Getting update failed.")
				continue
			}

			updates = append(updates, u...)
		}

	}()

	// Watch for next 2 seconds and try to reconstruct state.
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
	}
	cancel()
	watcher.Close()

	addresses := map[string]struct{}{}
	for _, u := range updates {
		if u.Op == naming.Add {
			addresses[u.Addr] = struct{}{}
		}
		if u.Op == naming.Delete {
			delete(addresses, u.Addr)
		}
	}

	logger.Infof("Resolved Addresses: %v", addresses)
}
