package kedge_map

type RouteMatcher interface {
	Match(dns string) bool
	Route(dns string) (*Route, error)
}

type routeMapper struct {
	routes []RouteMatcher
}

// RouteMapper is a mapper that resolves Route based on given DNS.
func RouteMapper(r []RouteMatcher) *routeMapper {
	return &routeMapper{
		routes: r,
	}
}

func (m *routeMapper) Map(targetAuthorityDnsName string) (*Route, error) {
	for _, route := range m.routes {
		if !route.Match(targetAuthorityDnsName) {
			continue
		}

		return route.Route(targetAuthorityDnsName)
	}

	return nil, ErrNotKedgeDestination
}
