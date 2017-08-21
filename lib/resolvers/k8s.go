package resolvers

import (
	"github.com/Bplotka/go-k8sresolver"
	pb "github.com/mwitkow/kedge/_protogen/kedge/config/common/resolvers"
	"google.golang.org/grpc/naming"
)

func NewK8sFromConfig(conf *pb.K8SResolver) (target string, name naming.Resolver, err error) {
	resolver, err := k8sresolver.NewFromFlags()
	if err != nil {
		return "", nil, err
	}
	return conf.GetDnsPortName(), resolver, nil
}
