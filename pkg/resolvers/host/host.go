package hostresolver

import (
	"fmt"
	"net"
	"time"

	grpcsrvlb "github.com/improbable-eng/go-srvlb/grpc"
	"github.com/improbable-eng/go-srvlb/srv"
	pb "github.com/improbable-eng/kedge/protogen/kedge/config/common/resolvers"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/naming"
)

type hostResolverFn func(host string) (addrs []string, err error)

var (
	ParentHostResolver hostResolverFn = net.LookupHost

	resolutions = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "kedge",
			Subsystem: "host_resolver",
			Name:      "resolutions_attempts_total",
			Help:      "Counter of all DNS resolutions attempts made by host resolver.",
		},
	)

	// TODO(bwplotka): Use real TTL for this. https://github.com/improbable-eng/kedge/issues/147
	resolutionTTL = 5 * time.Second
)

func init() {
	prometheus.MustRegister(resolutions)
}

// NewFromConfig creates HOST resolver wrapped by grpcsrvlb that polls host resolver in frequency defined by returned TTL.
func NewFromConfig(conf *pb.HostResolver) (target string, namer naming.Resolver, err error) {
	parent := ParentHostResolver
	return conf.GetDnsName(), grpcsrvlb.New(newHostResolver(conf.Port, parent)), nil
}

type hostResolver struct {
	hostResolverFn hostResolverFn
	port           uint32
}

// newHostResolver uses results from parent resolver and adds a port to it to implement srv.Resolver.
func newHostResolver(port uint32, hostResolverFn hostResolverFn) srv.Resolver {
	return &hostResolver{
		hostResolverFn: hostResolverFn,
		port:           port,
	}
}

func (r *hostResolver) Lookup(domainName string) ([]*srv.Target, error) {
	ips, err := r.hostResolverFn(domainName)
	resolutions.Inc()
	if err != nil {
		return nil, err
	}

	var targets []*srv.Target
	for _, ip := range ips {
		targets = append(targets, &srv.Target{
			DialAddr: net.JoinHostPort(ip, fmt.Sprintf("%d", r.port)),
			Ttl:      resolutionTTL,
		})
	}

	return targets, nil
}
