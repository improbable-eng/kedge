package resolvers

import (
	pb "github.com/mwitkow/kfe/_protogen/kfe/config/common/resolvers"
	"google.golang.org/grpc/naming"
	"github.com/mwitkow/go-srvlb/grpc"
	"github.com/mwitkow/go-srvlb/srv"
	"time"
)

var (
	ParentSrvResolver = srv.NewGoResolver(5 * time.Second)
)

func NewSrvFromConfig(conf *pb.SrvResolver) (target string, namer naming.Resolver, err error) {
	return conf.GetDnsName(), grpcsrvlb.New(ParentSrvResolver), nil
}


