package router

import (
	"net/http"
	"net"
	"github.com/mwitkow/kedge/lib/resolvers"
	pb "github.com/mwitkow/kedge/_protogen/kedge/config/http/routes"
	"strings"
	"strconv"
	"fmt"
)

var (
	// DefaultALookup is the lookup resolver for DNS A records.
	// You can override it for caching or testing.
	DefaultALookup = net.LookupAddr
)


type AdhocAddresser interface {
	// Address decides the ip:port to send the request to, if any. Errors may be returned if permission is denied.
	// The returned string must contain contain both ip and port separated by colon.
	Address(r *http.Request) (string, error)
}


type addresser struct {
	rules []*pb.Adhoc
}



func NewAddresser(rules []*pb.Adhoc) AdhocAddresser {
	return &addresser{rules: rules}
}

func (a *addresser) Address(req *http.Request) (string, error) {
	requestPort := 0
	if _, pStr, err := net.SplitHostPort(req.URL.Host); err != nil {
		if ! strings.Contains(err.Error(), "missing port in address") {
			return "", NewError(http.StatusBadRequest, "malformed port number '" + pStr + "'")
		}
	}
	else {
		if pNum, err := strconv.ParseInt(pStr, 10, 16); err != nil {
			return "", NewError(http.StatusBadRequest, "malformed port number '" + pStr + "'")
		} else {
			requestPort = int(pNum)
		}
	}
	for _, rule := range a.rules {
		if !a.hostMatches(req.URL.Host, rule.DnsNameMatcher) {
			continue
		}
		portForRule := requestPort
		if requestPort == 0 {
			if defPort := rule.Port.Default; defPort != 0 {
				portForRule = int(defPort)
			} else {
				portForRule = 80
			}
		}
		if !a.portAllowed(portForRule, rule.Port) {
			return "", NewError(http.StatusBadRequest, fmt.Sprintf("port %d is not allowed", portForRule))
		}
		DefaultALookup(fmt.Sprintf(""))



	}
	return "", ErrRouteNotFound
}

func (*addresser) hostMatches(host string, matcher string) bool {
	if matcher == "" {
		return false
	}
	if matcher[0] != '*' {
		return host == matcher
	}
	return host == matcher[1:]
}

func (*addresser) portAllowed(port int, portRule *pb.Adhoc_Port) bool {

}
