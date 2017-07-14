package kedge_map

type RouteGetter interface {
	Route(dns string) (*Route, bool, error)
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

func (m *routeMapper) Map(targetAuthorityDnsName string) (*Route, error) {
	for _, route := range m.routes {
		r, ok, err := route.Route(targetAuthorityDnsName)
		if err != nil {
			return nil, err
		}

		if !ok {
			continue
		}

		return r, nil
	}

	return nil, ErrNotKedgeDestination
}
