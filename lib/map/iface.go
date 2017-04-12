package kedge_map

import (
	"errors"
	"net/url"
)

var (
	ErrNotKedgeDestination = errors.New("not a kedge destination")
)

// Mapper is an interface that allows you to direct traffic to different kedges.
// These are used by client libraries.
type Mapper interface {
	// Map maps a target's DNS name (e.g. myservice.prod.ext.europe-cluster.local) to a (public) URL of the Kedge
	// fronting that destination. The returned Scheme is deciding whether the Kedge connection is secure.
	// If the targets shouldn't go through a kedge, ErrNotKedgeDestination should be returned.
	Map(targetAuthorityDnsName string) (*url.URL, error)
}

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
