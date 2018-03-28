package kedge_map

import (
	"fmt"
	"regexp"

	"github.com/pkg/errors"
)

type RouteGetter interface {
	Route(hostPort string) (*Route, bool, error)
}

type routeMapper struct {
	routes        []RouteGetter
	ipMatchRegexp *regexp.Regexp
}

// RouteMapper is a mapper that resolves Route based on given DNS.
func RouteMapper(r []RouteGetter) *routeMapper {
	ipMatchRegexp, err := regexp.Compile(`^\d+\.\d+\.\d+\.\d+$`)
	if err != nil {
		// This should never panic in production, only in unit tests.
		panic(fmt.Sprintf("failed to compile regular expression: %v", err))
	}
	return &routeMapper{
		routes:        r,
		ipMatchRegexp: ipMatchRegexp,
	}
}

func (m *routeMapper) Map(targetDnsName string, targetPort string) (*Route, error) {
	if m.ipMatchRegexp.MatchString(targetDnsName) {
		return nil, errors.Errorf("kedge requires hostname to proxy to but was given IP address %s:%s instead", targetDnsName, targetPort)
	}

	hostPort := targetDnsName
	if targetPort != "" {
		hostPort = fmt.Sprintf("%s:%s", targetDnsName, targetPort)
	}
	for _, route := range m.routes {
		r, ok, err := route.Route(hostPort)
		if err != nil {
			return nil, err
		}

		if !ok {
			continue
		}

		return r, nil
	}

	return nil, NotKedgeDestinationErr(targetDnsName, targetPort)
}
