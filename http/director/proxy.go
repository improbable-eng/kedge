package director

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"time"

	"github.com/Bplotka/oidc/authorize"
	"github.com/mwitkow/go-conntrack"
	"github.com/mwitkow/go-httpwares"
	"github.com/mwitkow/go-httpwares/tags"
	"github.com/mwitkow/kedge/http/backendpool"
	"github.com/mwitkow/kedge/http/director/adhoc"
	"github.com/mwitkow/kedge/http/director/proxyreq"
	"github.com/mwitkow/kedge/http/director/router"
	"github.com/mwitkow/kedge/lib/http/ctxtags"
	"github.com/mwitkow/kedge/lib/http/tripperware"
	"github.com/mwitkow/kedge/lib/sharedflags"
	"github.com/oxtoacart/bpool"
	"github.com/sirupsen/logrus"
)

var (
	AdhocTransport = &(*(http.DefaultTransport.(*http.Transport))) // shallow copy

	flagBufferSizeBytes  = sharedflags.Set.Int("http_reverseproxy_buffer_size_bytes", 32*1024, "Size (bytes) of reusable buffer used for copying HTTP reverse proxy responses.")
	flagBufferCount      = sharedflags.Set.Int("http_reverseproxy_buffer_count", 2*1024, "Maximum number of of reusable buffer used for copying HTTP reverse proxy responses.")
	flagFlushingInterval = sharedflags.Set.Duration("http_reverseproxy_flushing_interval", 10*time.Millisecond, "Interval for flushing the responses in HTTP reverse proxy code.")
)

// New creates a forward/reverse proxy that is either Route+Backend and Adhoc Rules forwarding.
//
// The Router decides which "well-known" routes a given request matches, and which backend from the Pool it should be
// sent to. The backends in the Pool have pre-dialed connections and are load balanced.
//
// If  Adhoc routing supports dialing to whitelisted DNS names either through DNS A or SRV records for undefined backends.
func New(pool backendpool.Pool, router router.Router, addresser adhoc.Addresser) *Proxy {
	adhocTripper := &(*AdhocTransport) // shallow copy
	adhocTripper.DialContext = conntrack.NewDialContextFunc(conntrack.DialWithName("adhoc"), conntrack.DialWithTracing())
	bufferpool := bpool.NewBytePool(*flagBufferCount, *flagBufferSizeBytes)
	p := &Proxy{
		backendReverseProxy: &httputil.ReverseProxy{
			Director:      func(r *http.Request) {},
			Transport:     &backendPoolTripper{pool: pool},
			FlushInterval: *flagFlushingInterval,
			BufferPool:    bufferpool,
		},
		adhocReverseProxy: &httputil.ReverseProxy{
			Director:      func(r *http.Request) {},
			Transport:     adhocTripper,
			FlushInterval: *flagFlushingInterval,
			BufferPool:    bufferpool,
		},
		router:    router,
		addresser: addresser,
	}
	return p
}

// Proxy is a forward/reverse proxy that implements Route+Backend and Adhoc Rules forwarding.
type Proxy struct {
	router    router.Router
	addresser adhoc.Addresser

	backendReverseProxy *httputil.ReverseProxy
	adhocReverseProxy   *httputil.ReverseProxy
}

func (p *Proxy) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
	if _, ok := resp.(http.Flusher); !ok {
		panic("the http.ResponseWriter passed must be an http.Flusher")
	}

	// note resp needs to implement Flusher, otherwise flush intervals won't work.
	normReq := proxyreq.NormalizeInboundRequest(req)
	backend, err := p.router.Route(req)
	tags := http_ctxtags.ExtractInbound(req)
	tags.Set(http_ctxtags.TagForCallService, "proxy")
	if err == nil {
		resp.Header().Set("x-kedge-backend-name", backend)
		tags.Set(ctxtags.TagsForProxyBackend, backend)
		tags.Set(http_ctxtags.TagForHandlerName, backend)
		normReq.URL.Host = backend
		p.backendReverseProxy.ServeHTTP(resp, normReq)
		return
	} else if err != router.ErrRouteNotFound {
		respondWithError(err, req, resp)
		return
	}
	addr, err := p.addresser.Address(req)
	if err == nil {
		normReq.URL.Host = addr
		tags.Set(ctxtags.TagForProxyAdhoc, addr)
		tags.Set(http_ctxtags.TagForHandlerName, "_adhoc")
		p.adhocReverseProxy.ServeHTTP(resp, normReq)
		return
	}
	respondWithError(err, req, resp)
}

// backendPoolTripper assumes the response has been rewritten by the proxy to have the backend as req.URL.Host
type backendPoolTripper struct {
	pool backendpool.Pool
}

func (t *backendPoolTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	tripper, err := t.pool.Tripper(req.URL.Host)
	if err == nil {
		return tripper.RoundTrip(req)
	}
	return nil, err
}

func respondWithError(err error, req *http.Request, resp http.ResponseWriter) {
	status := http.StatusBadGateway
	if rErr, ok := (err).(*router.Error); ok {
		status = rErr.StatusCode()
	}
	http_ctxtags.ExtractInbound(req).Set(logrus.ErrorKey, err)
	resp.Header().Set("x-kedge-error", err.Error())
	resp.Header().Set("content-type", "text/plain")
	resp.WriteHeader(status)
	fmt.Fprintf(resp, "kedge error: %v", err.Error())
}

func AuthMiddleware(authorizer authorize.Authorizer) httpwares.Middleware {
	return func(nextHandler http.Handler) http.Handler {
		return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
			err := authorize.IsRequestAuthorized(req, authorizer, tripperware.ProxyAuthHeader)
			if err != nil {
				respondWithUnauthorized(err, req, resp)
				return
			}
			// Request authorized - continue.
			nextHandler.ServeHTTP(resp, req)
		})
	}
}

func respondWithUnauthorized(err error, req *http.Request, resp http.ResponseWriter) {
	status := http.StatusUnauthorized
	http_ctxtags.ExtractInbound(req).Set(logrus.ErrorKey, err)
	resp.Header().Set("x-kedge-error", err.Error())
	resp.Header().Set("content-type", "text/plain")
	resp.WriteHeader(status)
	// print?
	fmt.Fprintf(resp, "kedge auth error: %v", err.Error())
}
