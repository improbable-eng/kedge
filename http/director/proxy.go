package director

import (
	"net/http"
	"net/http/httputil"

	"fmt"

	"github.com/Sirupsen/logrus"
	"github.com/mwitkow/go-conntrack"
	"github.com/mwitkow/kedge/http/backendpool"
	"github.com/mwitkow/kedge/http/director/proxyreq"
	"github.com/mwitkow/kedge/http/director/router"
	"github.com/mwitkow/kedge/http/middleware/logging/logrus"
)

var (
	AdhocTransport = &(*(http.DefaultTransport.(*http.Transport))) // shallow copy
)

// New creates a forward/reverse proxy that is either Route+Backend and Adhoc Rules forwarding.
//
// The Router decides which "well-known" routes a given request matches, and which backend from the Pool it should be
// sent to. The backends in the Pool have pre-dialed connections and are load balanced.
//
// If  Adhoc routing supports dialing to whitelisted DNS names either through DNS A or SRV records for undefined backends.
func New(pool backendpool.Pool, router router.Router, adhoc router.AdhocAddresser) *Proxy {
	adhocTripper := &(*AdhocTransport) // shallow copy
	adhocTripper.DialContext = conntrack.NewDialContextFunc(conntrack.DialWithName("adhoc"), conntrack.DialWithTracing())
	p := &Proxy{
		backendReverseProxy: &httputil.ReverseProxy{
			Director:  func(r *http.Request) {},
			Transport: &backendPoolTripper{pool: pool},
		},
		adhocReverseProxy: &httputil.ReverseProxy{
			Director:  func(r *http.Request) {},
			Transport: adhocTripper,
		},
		router:    router,
		addresser: adhoc,
	}
	return p
}

// Proxy is a forward/reverse proxy that implements Route+Backend and Adhoc Rules forwarding.
type Proxy struct {
	router    router.Router
	addresser router.AdhocAddresser

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
	if err == nil {
		resp.Header().Set("x-kedge-backend-name", backend)
		normReq.URL.Host = backend
		http_logrus.AddFields(req.Context(), logrus.Fields{"kedge_backend": backend})
		p.backendReverseProxy.ServeHTTP(resp, normReq)
		return
	} else if err != router.ErrRouteNotFound {
		respondWithError(err, req, resp)
		return
	}
	addr, err := p.addresser.Address(req)
	if err == nil {
		normReq.URL.Host = addr
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
	http_logrus.AddFields(req.Context(), logrus.Fields{logrus.ErrorKey: err})
	resp.Header().Set("x-kedge-error", err.Error())
	resp.Header().Set("content-type", "text/plain")
	resp.WriteHeader(status)
	fmt.Fprintf(resp, "kedge error: %v", err.Error())
}
