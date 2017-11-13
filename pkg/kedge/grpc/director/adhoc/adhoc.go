package adhoc

import (
	"net"
	"strconv"

	"github.com/improbable-eng/kedge/pkg/kedge/common"
	"github.com/improbable-eng/kedge/pkg/kedge/grpc/director/router"
	"github.com/improbable-eng/kedge/protogen/kedge/config/common"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

type static struct {
	rules []*kedge_config_common.Adhoc
}

func NewStaticAddresser(rules []*kedge_config_common.Adhoc) *static {
	return &static{rules: rules}
}

func (a *static) Address(hostString string) (string, error) {
	hostName, port, err := common.ExtractHostPort(hostString)
	if err != nil {
		return "", grpc.Errorf(codes.InvalidArgument, "adhoc: malformed port number: %v", err)
	}
	for _, rule := range a.rules {
		if !common.HostMatches(hostName, rule.DnsNameMatcher) {
			continue
		}
		portForRule := port
		if port == 0 {
			if defPort := rule.Port.Default; defPort != 0 {
				portForRule = int(defPort)
			} else {
				portForRule = 81
			}
		}
		if !common.PortAllowed(portForRule, rule.Port) {
			return "", grpc.Errorf(codes.InvalidArgument, "adhoc: port %d is not allowed", portForRule)
		}
		ipAddr, err := common.ResolveHost(hostName)
		if err != nil {
			return "", grpc.Errorf(codes.NotFound, "adhoc: cannot resolve %s host: %v", hostString, err)
		}
		return net.JoinHostPort(ipAddr, strconv.FormatInt(int64(portForRule), 10)), nil

	}
	return "", router.ErrRouteNotFound
}
