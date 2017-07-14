package kedge_map

import (
	"net/url"
)

type single struct {
	kedgeUrl *url.URL
}

// Single is a simplistic kedge mapper that forwards all traffic through the same kedge.
// No auth is involved.
func Single(kedgeUrl *url.URL) Mapper {
	return &single{kedgeUrl: kedgeUrl}
}

func (s *single) Map(targetAuthorityDnsName string) (*Route, error) {
	return &Route{URL: s.kedgeUrl}, nil
}
