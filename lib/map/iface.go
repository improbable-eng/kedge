package kedge_map

import "net/url"

type Mapper interface {
	// Map maps a target's DNS name (e.g. myservice.prod.ext.europe-cluster.local) to a (public) URL of the Kedge
	// fronting that destination. The returned Scheme is deciding whether the Kedge connection is secure.
	Map(targetAuthorityDnsName string) (*url.URL, error)
}

func Single(kedgeUrl *url.URL) Mapper {
	return &single{kedgeUrl}
}

type single struct {
	kedgeUrl *url.URL
}

func (s *single) Map(targetAuthorityDnsName string) (*url.URL, error) {
	return s.kedgeUrl, nil
}
