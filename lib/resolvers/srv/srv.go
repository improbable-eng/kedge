package srvresolver

import (
	"time"

	"github.com/improbable-eng/go-srvlb/grpc"
	"github.com/improbable-eng/go-srvlb/srv"
	pb "github.com/improbable-eng/kedge/_protogen/kedge/config/common/resolvers"
	"google.golang.org/grpc/naming"
	"strings"
	"net"
	"fmt"
)

var (
	ParentSrvResolver = srv.NewGoResolver(5 * time.Second)
)

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
		port: port,
	}
}

type portOverrideSRVResolver struct {
	parent srv.Resolver
	port uint32
}

func (r *portOverrideSRVResolver) Lookup(domainName string) ([]*srv.Target, error) {
	targets, err := r.parent.Lookup(domainName)
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