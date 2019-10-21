package srvresolver

import (
	"fmt"
	"net"
	"strings"
	"time"

	grpcsrvlb "github.com/improbable-eng/go-srvlb/grpc"
	"github.com/improbable-eng/go-srvlb/srv"
	pb "github.com/improbable-eng/kedge/protogen/kedge/config/common/resolvers"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/naming"
)

var (
	ParentSrvResolver = srv.NewGoResolver(resolutionTTL)

	resolutions = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "kedge",
			Subsystem: "srv_resolver",
			Name:      "resolutions_attempts_total",
			Help:      "Counter of all DNS resolutions attempts made by SRV resolver.",
		},
	)

	// TODO(bwplotka): Use real TTL for this. https://github.com/improbable-eng/kedge/issues/147
	resolutionTTL = 5 * time.Second
)

func init() {
	prometheus.MustRegister(resolutions)
}

// NewFromConfig creates SRV resolver wrapped by grpcsrvlb that polls SRV resolver in frequency defined by returned TTL.
func NewFromConfig(conf *pb.SrvResolver) (target string, namer naming.Resolver, err error) {
	parent := ParentSrvResolver
	if conf.PortOverride != 0 {
		parent = newPortOverrideSRVResolver(conf.PortOverride, parent)
	}

	return conf.GetDnsName(), grpcsrvlb.New(parent), nil
}

// newPortOverrideSRVResolver uses results from parent resolver, but ignores port totally and specifies our own.
func newPortOverrideSRVResolver(port uint32, resolver srv.Resolver) srv.Resolver {
	return &portOverrideSRVResolver{
		parent: resolver,
		port:   port,
	}
}

type portOverrideSRVResolver struct {
	parent srv.Resolver
	port   uint32
}

func (r *portOverrideSRVResolver) Lookup(domainName string) ([]*srv.Target, error) {
	targets, err := r.parent.Lookup(domainName)
	resolutions.Inc()
	if err != nil {
		return targets, err
	}

	for _, target := range targets {
		splitted := strings.Split(target.DialAddr, ":")

		// Ignore port from SRV and use specified one.
		target.DialAddr = net.JoinHostPort(splitted[0], fmt.Sprintf("%d", r.port))
	}

	return targets, nil
}
