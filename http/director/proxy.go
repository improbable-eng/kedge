package director

import (
	"net/http"
	"net/http/httputil"

	"github.com/mwitkow/kedge/http/backendpool"
	"github.com/mwitkow/kedge/http/director/router"
	"github.com/mwitkow/kedge/http/director/proxyreq"
	"fmt"
)

func New(pool backendpool.Pool, router router.Router) *Proxy {
	p := &Proxy{
		reverseProxy: &httputil.ReverseProxy{
			Director: func(r *http.Request) {},
			Transport: &backendPoolTripper{pool: pool},
		},
		router: router,
	}
	return p
}

type Proxy struct {
	reverseProxy *httputil.ReverseProxy
	router       router.Router
}

func (p *Proxy) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
	// note resp needs to implement Flusher, otherwise flush intervals won't work.
	normReq := proxyreq.NormalizeInboundRequest(req)
	backend, err := p.router.Route(req)
	fmt.Printf("Got request: %v err %v", backend, err)
	if err != nil {
		resp.WriteHeader(http.StatusBadGateway)
		return
	}

	normReq.URL.Host = backend
	p.reverseProxy.ServeHTTP(resp, req)
}

// backendPoolTripper assumes the response has been rewritten by the proxy to have the backend as req.URL.Host
type backendPoolTripper struct {
	pool backendpool.Pool
}

func (t *backendPoolTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	backend := req.URL.Host
	tripper, err := t.pool.Tripper(backend)
	if err == nil {
		return tripper.RoundTrip(req)
	}
	return nil, err
}
