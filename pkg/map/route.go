package kedge_map

import "fmt"

type RouteGetter interface {
	Route(hostPort string) (*Route, bool, error)
}

type routeMapper struct {
	routes []RouteGetter
}

// RouteMapper is a mapper that resolves Route based on given DNS.
func RouteMapper(r []RouteGetter) *routeMapper {
	return &routeMapper{
		routes: r,
	}
}

func (m *routeMapper) Map(targetDnsName string, targetPort string) (*Route, error) {
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
