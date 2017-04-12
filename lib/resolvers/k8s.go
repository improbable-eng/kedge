package resolvers

import (
	pb "github.com/mwitkow/kedge/_protogen/kedge/config/common/resolvers"

	"fmt"

	"github.com/sercand/kuberesolver"
	"google.golang.org/grpc/naming"
)

func NewK8sFromConfig(conf *pb.KubeResolver) (target string, namer naming.Resolver, err error) {
	// see https://github.com/sercand/kuberesolver/blob/master/README.md
	target = fmt.Sprintf("kubernetes://%v:%v", conf.ServiceName, conf.PortName)
	namespace := "default"
	if conf.Namespace != "" {
		namespace = conf.Namespace
	}
	b := kuberesolver.NewWithNamespace(namespace)
	return target, b.Resolver(), nil
}
