package director

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httputil"
	"time"

	"github.com/Bplotka/oidc/authorize"
	"github.com/mwitkow/go-conntrack"
	"github.com/improbable-eng/go-httpwares"
	"github.com/improbable-eng/go-httpwares/logging/logrus"
	"github.com/improbable-eng/go-httpwares/metrics"
	"github.com/improbable-eng/go-httpwares/metrics/prometheus"
	"github.com/improbable-eng/go-httpwares/tags"
	"github.com/improbable-eng/kedge/http/backendpool"
	"github.com/improbable-eng/kedge/http/director/adhoc"
	"github.com/improbable-eng/kedge/http/director/proxyreq"
	"github.com/improbable-eng/kedge/http/director/router"
	"github.com/improbable-eng/kedge/lib/http/ctxtags"
	"github.com/improbable-eng/kedge/lib/http/tripperware"
	"github.com/improbable-eng/kedge/lib/reporter"
	"github.com/improbable-eng/kedge/lib/reporter/errtypes"
	"github.com/improbable-eng/kedge/lib/sharedflags"
	"github.com/oxtoacart/bpool"
	"github.com/sirupsen/logrus"
)

var (
	AdhocTransport = &http.Transport{
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
	}

	flagBufferSizeBytes  = sharedflags.Set.Int("http_reverseproxy_buffer_size_bytes", 32*1024, "Size (bytes) of reusable buffer used for copying HTTP reverse proxy responses.")
	flagBufferCount      = sharedflags.Set.Int("http_reverseproxy_buffer_count", 2*1024, "Maximum number of of reusable buffer used for copying HTTP reverse proxy responses.")
	flagFlushingInterval = sharedflags.Set.Duration("http_reverseproxy_flushing_interval", 10*time.Millisecond, "Interval for flushing the responses in HTTP reverse proxy code.")
)

// New creates a forward/reverse proxy that is either Route+Backend and Adhoc Rules forwarding.
//
// The Router decides which "well-known" routes a given request matches, and which backend from the Pool it should be
// sent to. The backends in the Pool have pre-dialed connections and are load balanced.
//
// If Adhoc routing supports dialing to whitelisted DNS names either through DNS A or SRV records for undefined backends.
func New(pool backendpool.Pool, router router.Router, addresser adhoc.Addresser, logEntry logrus.FieldLogger) *Proxy {
	AdhocTransport.DialContext = conntrack.NewDialContextFunc(conntrack.DialWithName("adhoc"), conntrack.DialWithTracing())
	bufferpool := bpool.NewBytePool(*flagBufferCount, *flagBufferSizeBytes)

	clientMetrics := http_prometheus.ClientMetrics()
	backendTripper := http_metrics.Tripperware(clientMetrics)(&backendPoolTripper{pool: pool})
	adhocTripper := http_metrics.Tripperware(clientMetrics)(AdhocTransport)

	backendErrLog := http_logrus.AsHttpLogger(logEntry.WithField("caller", "backend reverseProxy"))
	adhocErrLog := http_logrus.AsHttpLogger(logEntry.WithField("caller", "adhoc reverseProxy"))
	p := &Proxy{
		backendReverseProxy: &httputil.ReverseProxy{
			Director:      func(*http.Request) {},
			Transport:     reverseProxyErrHandler(backendTripper, logEntry.WithField("caller", "backend reverseProxy error handler")),
			FlushInterval: *flagFlushingInterval,
			BufferPool:    bufferpool,
			ErrorLog:      backendErrLog,
		},
		adhocReverseProxy: &httputil.ReverseProxy{
			Director:      func(*http.Request) {},
			Transport:     reverseProxyErrHandler(adhocTripper, logEntry.WithField("caller", "adhoc reverseProxy error handler")),
			FlushInterval: *flagFlushingInterval,
			BufferPool:    bufferpool,
			ErrorLog:      adhocErrLog,
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
	// Note that resp needs to implement Flusher, otherwise flush intervals won't work.
	// We can panic, unit tests will catch that.
	if _, ok := resp.(http.Flusher); !ok {
		panic("the http.ResponseWriter passed must be an http.Flusher")
	}

	normReq := proxyreq.NormalizeInboundRequest(req)
	tags := http_ctxtags.ExtractInbound(req)
	tags.Set(http_ctxtags.TagForCallService, "proxy")

	// Perform routing.
	// We can have one of these 4 cases:
	// - backend routing
	// - adhoc routing
	// - unknown route to host
	// - routing error

	backend, err := p.router.Route(req)
	if err == router.ErrRouteNotFound {
		// Try adhoc.
		var addr string
		addr, err = p.addresser.Address(req)
		if err == nil {
			normReq.URL.Host = addr
			tags.Set(ctxtags.TagForProxyAdhoc, addr)
			tags.Set(http_ctxtags.TagForHandlerName, "_adhoc")
			p.adhocReverseProxy.ServeHTTP(resp, normReq)
			return
		}
	}

	if err == nil {
		resp.Header().Set("x-kedge-backend-name", backend)
		tags.Set(ctxtags.TagForProxyBackend, backend)
		tags.Set(http_ctxtags.TagForHandlerName, backend)
		normReq.URL.Host = backend
		p.backendReverseProxy.ServeHTTP(resp, normReq)
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
	if err != nil {
		reporter.Extract(req).ReportError(errtypes.NoBackend, err)
		return nil, err
	}
	return tripper.RoundTrip(req)
}

func respondWithError(err error, req *http.Request, resp http.ResponseWriter) {
	errType := errtypes.RouteUnknownError
	if err == router.ErrRouteNotFound {
		errType = errtypes.NoRoute
	}
	tracker := reporter.Extract(req)
	tracker.ReportError(errType, err)

	status := http.StatusBadGateway
	if rErr, ok := (err).(*router.Error); ok {
		status = rErr.StatusCode()
	}
	http_ctxtags.ExtractInbound(req).Set(logrus.ErrorKey, err)
	reporter.SetKedgeErrorHeaders(resp.Header(), tracker)
	resp.Header().Set("content-type", "text/plain")
	resp.WriteHeader(status)
	fmt.Fprintf(resp, "%v", err.Error())
}

func AuthMiddleware(authorizer authorize.Authorizer) httpwares.Middleware {
	return func(nextHandler http.Handler) http.Handler {
		return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
			err := authorize.IsRequestAuthorized(req, authorizer, tripperware.ProxyAuthHeader)
			if err != nil {
				respondWithUnauthorized(err, req, resp)
				return
			}

			// Strip out ProxyAuth header.
			req.Header.Del(tripperware.ProxyAuthHeader)

			// Request authorized - continue.
			nextHandler.ServeHTTP(resp, req)
		})
	}
}

func respondWithUnauthorized(err error, req *http.Request, resp http.ResponseWriter) {
	errType := errtypes.Unauthorized
	reporter.Extract(req).ReportError(errType, err)

	status := http.StatusUnauthorized
	http_ctxtags.ExtractInbound(req).Set(logrus.ErrorKey, err)
	reporter.SetKedgeErrorHeaders(resp.Header(), reporter.Extract(req))
	resp.Header().Set("content-type", "text/plain")
	resp.WriteHeader(status)
	fmt.Fprintln(resp, "Unauthorized")
}

func reverseProxyErrHandler(next http.RoundTripper, logEntry logrus.FieldLogger) http.RoundTripper {
	return httpwares.RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		t := reporter.Extract(req)
		resp, err := next.RoundTrip(req)
		if err != nil {
			if resp == nil {
				resp = &http.Response{
					Request:    req,
					Header:     http.Header{},
					Body:       ioutil.NopCloser(&bytes.Buffer{}),
					StatusCode: http.StatusBadGateway,
				}
			}
			reporter.SetKedgeErrorHeaders(resp.Header, t)
			// Mimick reverse proxy err handling.
			tags := http_ctxtags.ExtractInbound(req).Values()
			logEntry.WithFields(tags).WithError(err).Warn("HTTP roundTrip failed")
		}

		return resp, nil
	})
}
