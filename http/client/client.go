package kedge_http

import (
	"crypto/tls"
	"net/http"

	"github.com/mwitkow/kedge/lib/http/tripperware"
	"github.com/mwitkow/kedge/lib/map"
	"golang.org/x/net/http2"
)

func NewClient(mapper kedge_map.Mapper, clientTls *tls.Config, parentTransport *http.Transport) *http.Client {
	cloneTransport := &(*parentTransport)
	if clientTls != nil {
		cloneTransport.TLSClientConfig = clientTls
		if err := http2.ConfigureTransport(cloneTransport); err != nil {
			panic(err) // this should never happen, but let's not lose an error.
		}
	}
	return &http.Client{
		Transport: tripperware.WrapForMapping(mapper,
			tripperware.WrapForRouting(
				tripperware.DefaultWithTransport(cloneTransport, clientTls),
			),
		),
	}
}
