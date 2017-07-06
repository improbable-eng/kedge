package kedge_map

import "net/url"

// Single is a simplistic kedge mapper that forwards all traffic through the same kedge.
func Single(kedgeUrl *url.URL) Mapper {
	return &single{kedgeUrl}
}

type single struct {
	kedgeUrl *url.URL
}

func (s *single) Map(targetAuthorityDnsName string) (*url.URL, error) {
	return s.kedgeUrl, nil
}
