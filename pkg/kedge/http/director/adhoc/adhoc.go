package adhoc

import (
	"fmt"
	"net"
	"net/http"
	"strconv"

	"github.com/improbable-eng/kedge/pkg/kedge/common"
	"github.com/improbable-eng/kedge/pkg/kedge/http/director/router"
	"github.com/improbable-eng/kedge/protogen/kedge/config/common"
)

type static struct {
	rules []*kedge_config_common.Adhoc
}

func NewStaticAddresser(rules []*kedge_config_common.Adhoc) *static {
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

		ipAddr, err := common.AdhocResolveHost(hostName, rule.DnsNameReplace)
		if err != nil {
			return "", router.NewError(http.StatusBadGateway, fmt.Sprintf("adhoc: cannot resolve %s host: %v", hostPort, err))
		}
		return net.JoinHostPort(ipAddr, strconv.FormatInt(int64(portForRule), 10)), nil

	}
	return "", router.ErrRouteNotFound
}
