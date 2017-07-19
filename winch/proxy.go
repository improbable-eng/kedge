package winch

import (
	"crypto/tls"
	"net/http"
	"net/http/httputil"
	"time"

	"github.com/mwitkow/go-httpwares/tags"
	"github.com/mwitkow/kedge/http/director/proxyreq"
	"github.com/mwitkow/kedge/lib/http/tripperware"
	"github.com/mwitkow/kedge/lib/map"
	"github.com/mwitkow/kedge/lib/sharedflags"
	"github.com/oxtoacart/bpool"
)

var (
	flagBufferSizeBytes  = sharedflags.Set.Int("winch_reverseproxy_buffer_size_bytes", 32*1024, "Size (bytes) of reusable buffer used for copying HTTP reverse proxy responses.")
	flagBufferCount      = sharedflags.Set.Int("winch_reverseproxy_buffer_count", 2*1024, "Maximum number of of reusable buffer used for copying HTTP reverse proxy responses.")
	flagFlushingInterval = sharedflags.Set.Duration("winch_reverseproxy_flushing_interval", 10*time.Millisecond, "Interval for flushing the responses in HTTP reverse proxy code.")
)

type winchMapper interface {
	kedge_map.Mapper
}

func New(mapper winchMapper, config *tls.Config) *Proxy {
	// Prepare chain of trippers for winch logic. (The last wrapped will be first in the chain of tripperwares)
	// 5) Last, default transport for communication with our kedges.
	// 4) Kedge auth tipper - injects auth for kedge based on route.
	// 3) Backend auth tripper - injects auth for backend based on route.
	// 2) Routing tripper - redirects to kedge if specified based on route.
	// 1) First, mapping tripper - maps dns to route and puts it to request context for rest of the tripperwares.

	parentTransport := tripperware.Default(config)
	parentTransport = tripperware.WrapForProxyAuth(parentTransport)
	parentTransport = tripperware.WrapForBackendAuth(parentTransport)
	parentTransport = tripperware.WrapForRouting(parentTransport)
	parentTransport = tripperware.WrapForMapping(mapper, parentTransport)

	bufferpool := bpool.NewBytePool(*flagBufferCount, *flagBufferSizeBytes)
	return &Proxy{
		kedgeReverseProxy: &httputil.ReverseProxy{
			Director:      func(r *http.Request) {},
			Transport:     parentTransport,
			FlushInterval: *flagFlushingInterval,
			BufferPool:    bufferpool,
		},
	}
}

// Proxy is a forward/reverse proxy that implements Mapper+Kedge forwarding.
type Proxy struct {
	kedgeReverseProxy *httputil.ReverseProxy
}

func (p *Proxy) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
	if _, ok := resp.(http.Flusher); !ok {
		panic("the http.ResponseWriter passed must be an http.Flusher")
	}

	if req.URL.Scheme == "" {
		// Local resource was requested and was not in previous route pattern.
		http.NotFound(resp, req)
		return
	}

	normReq := proxyreq.NormalizeInboundRequest(req)
	tags := http_ctxtags.ExtractInbound(req)
	tags.Set(http_ctxtags.TagForCallService, "winch")

	p.kedgeReverseProxy.ServeHTTP(resp, normReq)
}
