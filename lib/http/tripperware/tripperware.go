package tripperware

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

type defaultTripper struct {
	*http.Transport
}

func Default(config *tls.Config) http.RoundTripper {
	// Clone transport before using it. We don't want to modify the default one.
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
			DualStack: true,
		}).DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       config,
	}
	return &defaultTripper{Transport: transport}
}

func DefaultWithTransport(transport *http.Transport, config *tls.Config) http.RoundTripper {
	transport.TLSClientConfig = config
	return &defaultTripper{Transport: transport}
}
