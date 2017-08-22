package resolvers

import (
	"github.com/Bplotka/go-k8sresolver"
	pb "github.com/mwitkow/kedge/_protogen/kedge/config/common/resolvers"
	"google.golang.org/grpc/naming"
	"github.com/sirupsen/logrus"
)

func NewK8sFromConfig(conf *pb.K8SResolver) (target string, name naming.Resolver, err error) {
	logger := logrus.New().WithField("target", conf.GetDnsPortName())
	resolver, err := k8sresolver.NewFromFlags(logger)
	if err != nil {
		return "", nil, err
	}
	return conf.GetDnsPortName(), resolver, nil
}
