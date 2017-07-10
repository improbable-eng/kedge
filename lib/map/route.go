package kedge_map

import "net/url"

type Route interface {
	Match(dns string) bool
	URL(dns string) (*url.URL, error)
}

type Routes interface {
	Get() []Route
}

type routeMapper struct {
	r Routes
}

// RouteMapper is a mapper that resolves URL based on given routes.
func RouteMapper(r Routes) Mapper {
	return &routeMapper{
		r: r,
	}
}
func (m *routeMapper) Map(targetAuthorityDnsName string) (*url.URL, error) {
	routes := m.r.Get()
	for _, route := range routes {
		if !route.Match(targetAuthorityDnsName) {
			continue
		}

		return route.URL(targetAuthorityDnsName)
	}

	return nil, ErrNotKedgeDestination
}
