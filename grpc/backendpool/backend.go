package backendpool

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"sync"

	"github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/mwitkow/go-conntrack"
	"github.com/mwitkow/go-grpc-middleware"
	"github.com/mwitkow/grpc-proxy/proxy"
	pb "github.com/mwitkow/kfe/_protogen/kfe/config/grpc/backends"
	"github.com/mwitkow/kfe/lib/resolvers"
	"google.golang.org/grpc"
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
	cc, err := buildClientConn(b.config)
	if err != nil {
		return nil, err
	}
	b.conn = cc
	return cc, nil
}

func (b *backend) Close() error {
	return b.conn.Close()
}

func newBackend(cnf *pb.Backend) (*backend, error) {

	cc, err := buildClientConn(cnf)
	if err != nil && err.Error() == "grpc: there is no address available to dial" {
		return &backend{conn: nil, config: cnf}, nil // make this lazy
	} else if err != nil {
		return nil, fmt.Errorf("backend '%v' dial error: %v", cnf.Name, err)
	}
	return &backend{conn: cc, config: cnf}, nil
}

func buildClientConn(cnf *pb.Backend) (*grpc.ClientConn, error) {
	opts := []grpc.DialOption{}
	target, resolver, err := chooseNamingResolver(cnf)
	if err != nil {
		return nil, err
	}
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
			conntrack.DialWithName("backend_"+cnf.Name),
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
		config := &tls.Config{InsecureSkipVerify: true}
		if !sec.InsecureSkipVerify {
			// TODO(mwitkow): add configuration TlsConfig fetching by name here.
		}
		return grpc.WithTransportCredentials(credentials.NewTLS(config))
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
		return resolvers.NewSrvFromConfig(s)
	} else if k := cnf.GetK8S(); k != nil {
		return resolvers.NewK8sFromConfig(k)
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
