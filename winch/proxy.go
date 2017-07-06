package winch

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httputil"
	"time"

	"github.com/mwitkow/go-httpwares/tags"
	"github.com/mwitkow/kedge/http/client"
	"github.com/mwitkow/kedge/http/director/proxyreq"
	"github.com/mwitkow/kedge/http/director/router"
	"github.com/mwitkow/kedge/lib/map"
	"github.com/mwitkow/kedge/lib/sharedflags"
	"github.com/oxtoacart/bpool"
	"github.com/sirupsen/logrus"
)

var (
	flagBufferSizeBytes  = sharedflags.Set.Int("http_reverseproxy_buffer_size_bytes", 32*1024, "Size (bytes) of reusable buffer used for copying HTTP reverse proxy responses.")
	flagBufferCount      = sharedflags.Set.Int("http_reverseproxy_buffer_count", 2*1024, "Maximum number of of reusable buffer used for copying HTTP reverse proxy responses.")
	flagFlushingInterval = sharedflags.Set.Duration("http_reverseproxy_flushing_interval", 10*time.Millisecond, "Interval for flushing the responses in HTTP reverse proxy code.")
)

func New(mapper kedge_map.Mapper, config *tls.Config) *Proxy {
	// Prepare transport for communication with our kedges.
	parentTransport := http.DefaultTransport.(*http.Transport)
	parentTransport.TLSClientConfig = config


	bufferpool := bpool.NewBytePool(*flagBufferCount, *flagBufferSizeBytes)
	p := &Proxy{
		kedgeReverseProxy: &httputil.ReverseProxy{
			Director: func(r *http.Request) {},
			// Pass transport that will proxy to kedge in case of mapper match.
			Transport:      kedge_http.WrapTransport(mapper, parentTransport),
			FlushInterval: *flagFlushingInterval,
			BufferPool:    bufferpool,
		},
	}
	return p
}

// Proxy is a forward/reverse proxy that implements Mapper+Kedge forwarding.
type Proxy struct {
	kedgeReverseProxy *httputil.ReverseProxy
}

func (p *Proxy) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
	if _, ok := resp.(http.Flusher); !ok {
		panic("the http.ResponseWriter passed must be an http.Flusher")
	}

	// TODO(bplotka): Obtain fresh IDToken and pass as auth bearer in req.

	normReq := proxyreq.NormalizeInboundRequest(req)
	tags := http_ctxtags.ExtractInbound(req)
	tags.Set(http_ctxtags.TagForCallService, "proxy")

	p.kedgeReverseProxy.ServeHTTP(resp, normReq)
}

func respondWithError(err error, req *http.Request, resp http.ResponseWriter) {
	status := http.StatusBadGateway
	if rErr, ok := (err).(*router.Error); ok {
		status = rErr.StatusCode()
	}
	http_ctxtags.ExtractInbound(req).Set(logrus.ErrorKey, err)
	resp.Header().Set("x-winch-error", err.Error())
	resp.Header().Set("content-type", "text/plain")
	resp.WriteHeader(status)
	fmt.Fprintf(resp, "winch error: %v", err.Error())
}
