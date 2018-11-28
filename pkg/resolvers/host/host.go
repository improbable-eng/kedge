package hostresolver

import (
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
	"net"
	"os"
	"time"

	"github.com/improbable-eng/go-srvlb/grpc"
	"github.com/improbable-eng/go-srvlb/srv"
	pb "github.com/improbable-eng/kedge/protogen/kedge/config/common/resolvers"
	"google.golang.org/grpc/naming"
)

type hostResolverFn func(host string) (addrs []string, err error)

var (
	ParentHostResolver hostResolverFn = net.LookupHost
)

var (
	res = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "kedge",
			Subsystem: "dns_host",
			Name:      "resolutions",
			Help:      "debug.",
		},
		[]string{"target"},
	)
	resHist = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "kedge",
			Subsystem: "dns_host",
			Name:      "resolutions",
			Help:      "debug.",
		},
		[]string{"target"},
	)
)

func init() {
	prometheus.MustRegister(res)
	prometheus.MustRegister(resHist)
}

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
	res.WithLabelValues(domainName).Inc()
	start := time.Now()
	ips, err := r.hostResolverFn(domainName)
	resHist.WithLabelValues(domainName).Observe(float64(time.Since(start)))
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error", err)
		return nil, err
	}

	var targets []*srv.Target
	for _, ip := range ips {
		targets = append(targets, &srv.Target{
			DialAddr: net.JoinHostPort(ip, fmt.Sprintf("%d", r.port)),
		})
	}

	return targets, nil
}
