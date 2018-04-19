package adhoc

import (
	"fmt"
	"net"
	"net/http"
	"strconv"

	"github.com/improbable-eng/kedge/pkg/kedge/common"
	"github.com/improbable-eng/kedge/pkg/kedge/http/director/router"
	pb "github.com/improbable-eng/kedge/protogen/kedge/config/common"
)

type static struct {
	rules []*pb.Adhoc
}

func NewStaticAddresser(rules []*pb.Adhoc) *static {
	return &static{rules: rules}
}

func (a *static) Address(hostPort string) (string, error) {
	hostName, port, err := common.ExtractHostPort(hostPort)
	if err != nil {
		return "", router.NewError(http.StatusBadRequest, fmt.Sprintf("adhoc: malformed port number: %v", err))
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
				portForRule = 80
			}
		}
		if !common.PortAllowed(portForRule, rule.Port) {
			return "", router.NewError(http.StatusBadRequest, fmt.Sprintf("adhoc: port %d is not allowed", portForRule))
		}
		ipAddr, err := resolveHost(hostName)
		if err != nil {
			return "", err
		}
		return net.JoinHostPort(ipAddr, strconv.FormatInt(int64(portForRule), 10)), nil

	}
	return "", router.ErrRouteNotFound
}

func resolveHost(host string) (string, error) {
	addrs, err := common.DefaultALookup(host)
	if err != nil {
		return "", router.NewError(http.StatusBadGateway, fmt.Sprintf("adhoc: cannot resolve %s host: %v", host, err))
	}
	return addrs[0], nil
}
