package tripperware

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"time"
)

type defaultTripper struct {
	*http.Transport
}

func Default(config *tls.Config) http.RoundTripper {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		// TODO(bplotka): Switch back to DialCtx when it will be safe to use.
		// https://groups.google.com/forum/#!topic/golang-nuts/oiBBZfUb2hM
		Dial: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
			DualStack: false,
		}).Dial,
		MaxIdleConns:          4,
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

func closeIfNotNil(r io.Closer) {
	if r != nil {
		_ = r.Close()
	}
}
