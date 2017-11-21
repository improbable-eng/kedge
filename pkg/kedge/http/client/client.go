package kedge_http

import (
	"crypto/tls"
	"net/http"

	"github.com/improbable-eng/kedge/pkg/http/tripperware"
	"github.com/improbable-eng/kedge/pkg/map"
	"golang.org/x/net/http2"
)

// NewClient constructs new HTTP client that supports proxying by kedge.
// NOTE: No copy of parentTransport is done, so it will modify parent one.
func NewClient(mapper kedge_map.Mapper, clientTls *tls.Config, parentTransport *http.Transport) *http.Client {
	if clientTls != nil {
		parentTransport.TLSClientConfig = clientTls
		if err := http2.ConfigureTransport(parentTransport); err != nil {
			panic(err) // this should never happen, but let's not lose an error.
		}
	}
	return &http.Client{
		Transport: tripperware.WrapForMapping(mapper,
			tripperware.WrapForRouting(
				tripperware.DefaultWithTransport(parentTransport, clientTls),
			),
		),
	}
}
