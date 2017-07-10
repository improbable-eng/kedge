package kedge_http

import (
	"crypto/tls"
	"net/http"
	"strings"

	"github.com/mwitkow/kedge/lib/map"
	"golang.org/x/net/http2"
	"github.com/mwitkow/go-httpwares/tags"
)

// tripper is a piece of tripperware that dials certain destinations (indicated by a mapper) through a remote kedge.
// The dialing is performed in a "reverse proxy" fashion, where the Host header indicates to the kedge what backend
// to dial.
type tripper struct {
	mapper kedge_map.Mapper
	parent http.RoundTripper
}

func (t *tripper) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Host
	if strings.Contains(host, ":") {
		host = host[:strings.LastIndex(host, ":")]
	}

	destURL, err := t.mapper.Map(host)
	if err == kedge_map.ErrNotKedgeDestination {
		return t.parent.RoundTrip(req)
	}
	if err != nil {
		return nil, err
	}

	tags := http_ctxtags.ExtractInbound(req)
	tags.Set("http.proxy.kedge_url", destURL)
	tags.Set(http_ctxtags.TagForHandlerName, destURL)

	// Copy the URL and the request to not override the callers info.
	copyUrl := *req.URL
	copyUrl.Scheme = destURL.Scheme
	copyUrl.Host = destURL.Host
	copyReq := req.WithContext(req.Context()) // makes a copy
	copyReq.URL = &(copyUrl)                  // shallow copy
	copyReq.Host = host                       // store the original host
	return t.parent.RoundTrip(copyReq)
}

func NewClient(mapper kedge_map.Mapper, clientTls *tls.Config, parentTransport *http.Transport) *http.Client {
	cloneTransport := &(*parentTransport)
	if clientTls != nil {
		cloneTransport.TLSClientConfig = clientTls
		if err := http2.ConfigureTransport(cloneTransport); err != nil {
			panic(err) // this should never happen, but let's not lose an error.
		}
	}
	return &http.Client{
		Transport: &tripper{mapper: mapper, parent: cloneTransport},
	}
}

func WrapTransport(mapper kedge_map.Mapper, parentTransport *http.Transport) http.RoundTripper {
	cloneTransport := &(*parentTransport)
	return &tripper{mapper: mapper, parent: cloneTransport}
}
