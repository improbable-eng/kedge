package backendpool

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/mwitkow/go-conntrack"
	"github.com/mwitkow/go-httpwares"
	pb "github.com/mwitkow/kedge/_protogen/kedge/config/http/backends"
	"github.com/mwitkow/kedge/http/lbtransport"
	"github.com/mwitkow/kedge/lib/resolvers"
	"github.com/pkg/errors"
	"golang.org/x/net/http2"
	"google.golang.org/grpc/naming"
)

var (
	// Top DialContext func with decreased Dial Timeout in comparison to DefaultDialer.
	ParentDialFunc = (&net.Dialer{
		Timeout:   1 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext

	closedTripper = httpwares.RoundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("backend transport closed")
	})
)

type backend struct {
	mu sync.RWMutex

	transport *http.Transport
	tripper   http.RoundTripper
	config    *pb.Backend
	closed    bool
}

// Tripper returns tripper that should be used for this (and only this backend).
// It usually contains LoadBalancing logic inside.
func (b *backend) Tripper() http.RoundTripper {
	b.mu.RLock()
	t := b.tripper
	if b.closed {
		t = closedTripper
	}
	b.mu.RUnlock()
	return t
}

// Close is used when backend is removed from configuration dynamically.
func (b *backend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	if b.transport != nil {
		b.transport.CloseIdleConnections()
	}
	b.transport = nil
	return nil
}

// newBackend creates backend from given configuration.
func newBackend(cnf *pb.Backend) (*backend, error) {
	b := &backend{}
	target, resolver, err := chooseNamingResolver(cnf)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to construct resolver for backend %s", cnf.Name)
	}

	dialFunc := ParentDialFunc
	if !cnf.DisableConntracking {
		dialFunc = conntrack.NewDialContextFunc(
			conntrack.DialWithName("http_backend_"+cnf.Name),
			conntrack.DialWithDialContextFunc(dialFunc),
			conntrack.DialWithTracing(),
		)
	}

	scheme, tlsConfig := buildTls(cnf)
	b.transport = &http.Transport{
		DialContext:         dialFunc,
		TLSClientConfig:     tlsConfig,
		MaxIdleConnsPerHost: http.DefaultMaxIdleConnsPerHost, // We can possible increase that?
		MaxIdleConns:        4,
		// TODO(mwitkow): add idle conn configuration.
	}
	// We want there to be h2 on outbound SSL connections, this mangles tlsConfig
	if err := http2.ConfigureTransport(b.transport); err != nil {
		return nil, err
	}

	b.tripper, err = lbtransport.New(target, b.transport, resolver, chooseBalancerPolicy(cnf))
	if err != nil {
		return nil, err
	}
	b.tripper = buildTripperMiddlewareChain(cnf, b.tripper)
	b.tripper = &schemeTripper{expectedScheme: scheme, parent: b.tripper}
	return b, nil
}

func buildTls(cnf *pb.Backend) (scheme string, tlsConfig *tls.Config) {
	if sec := cnf.GetSecurity(); sec != nil {
		tlsConfig = &tls.Config{InsecureSkipVerify: true}
		if !sec.InsecureSkipVerify {
			// TODO(mwitkow): add configuration TlsConfig fetching by name here.
			panic("Not implemented") // Ugly but this matters.
		}
		return "https", tlsConfig
	} else {
		return "http", nil
	}
}

func buildTripperMiddlewareChain(cnf *pb.Backend, parent http.RoundTripper) http.RoundTripper {
	// TODO(mwitkow): Add tripper middleware
	return parent
}

func chooseNamingResolver(cnf *pb.Backend) (string, naming.Resolver, error) {
	if s := cnf.GetSrv(); s != nil {
		return resolvers.NewSrvFromConfig(s)
	} else if k := cnf.GetK8S(); k != nil {
		// TODO(mwitkow): Deal with HTTP URLs to resolver for K8s. It sets a target==kubernetes://. This may not work.
		return resolvers.NewK8sFromConfig(k)
	}
	return "", nil, fmt.Errorf("unspecified naming resolver for %v", cnf.Name)
}

func chooseBalancerPolicy(cnf *pb.Backend) lbtransport.LBPolicy {
	switch cnf.GetBalancer() {
	case pb.Balancer_ROUND_ROBIN:
		return lbtransport.RoundRobinPolicyFromFlags()
	default:
		return lbtransport.RoundRobinPolicyFromFlags()
	}
}

// SchemeTripper rewrites the request's proto scheme to enforce the backend properties.
type schemeTripper struct {
	expectedScheme string
	parent         http.RoundTripper
}

func (s *schemeTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = s.expectedScheme
	return s.parent.RoundTrip(req)
}
