package kedge_map

import (
	"net/url"

	"github.com/mwitkow/kedge/lib/tokenauth"
)

type single struct {
	route *Route
}

// Single is a simplistic kedge mapper that forwards all traffic through the same kedge.
// No auth is involved.
func Single(kedgeUrl *url.URL) Mapper {
	return &single{route: &Route{URL: kedgeUrl}}
}

// SingleWithProxyAuth is kedge mapper that always returns predefined route with given Proxy auth and no
// backend auth. Used for loadtest.
func SingleWithProxyAuth(kedgeUrl *url.URL, proxyAuth tokenauth.Source) Mapper {
	return &single{route: &Route{URL: kedgeUrl, ProxyAuth: proxyAuth}}
}

func (s *single) Map(_ string, _ string) (*Route, error) {
	r := *s.route
	return &r, nil
}
