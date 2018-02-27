package http_winch

import (
	"bytes"
	"crypto/tls"
	"errors"
	"io/ioutil"
	"net/http"
	"net/http/httputil"
	"time"

	"fmt"
	"io"
	"net"

	"github.com/improbable-eng/go-httpwares"
	"github.com/improbable-eng/go-httpwares/logging/logrus"
	"github.com/improbable-eng/go-httpwares/tags"
	"github.com/improbable-eng/kedge/pkg/http/header"
	"github.com/improbable-eng/kedge/pkg/http/tripperware"
	"github.com/improbable-eng/kedge/pkg/kedge/http/director/proxyreq"
	"github.com/improbable-eng/kedge/pkg/map"
	"github.com/improbable-eng/kedge/pkg/reporter"
	"github.com/improbable-eng/kedge/pkg/reporter/errtypes"
	"github.com/improbable-eng/kedge/pkg/sharedflags"
	"github.com/oxtoacart/bpool"
	"github.com/sirupsen/logrus"
)

var (
	flagBufferSizeBytes  = sharedflags.Set.Int("winch_reverseproxy_buffer_size_bytes", 32*1024, "Size (bytes) of reusable buffer used for copying HTTP reverse proxy responses.")
	flagBufferCount      = sharedflags.Set.Int("winch_reverseproxy_buffer_count", 2*1024, "Maximum number of of reusable buffer used for copying HTTP reverse proxy responses.")
	flagFlushingInterval = sharedflags.Set.Duration("winch_reverseproxy_flushing_interval", 10*time.Millisecond, "Interval for flushing the responses in HTTP reverse proxy code.")
)

type winchMapper interface {
	kedge_map.Mapper
}

func New(
	httpMapper winchMapper,
	grpcMapper winchMapper,
	localGRPCPort int,
	config *tls.Config,
	logEntry *logrus.Entry,
	mux *http.ServeMux,
	debugMode bool,
) *Proxy {

	// Prepare chain of trippers for winch logic. (The last wrapped will be first in the chain of tripperwares)
	// 5) Last, default transport for communication with our kedges.
	// 4) Kedge auth tipper - injects auth for kedge based on route.
	// 3) Backend auth tripper - injects auth for backend based on route.
	// 2) Routing tripper - redirects to kedge if specified based on route.
	// 1) First, mapping tripper - maps dns to route and puts it to request context for rest of the tripperwares.

	parentTransport := tripperware.Default(config)
	if debugMode {
		parentTransport = tripperware.WrapForDebug(parentTransport)
	}

	parentTransport = tripperware.WrapForProxyAuth(parentTransport)
	parentTransport = tripperware.WrapForBackendAuth(parentTransport)
	parentTransport = tripperware.WrapForRouting(parentTransport)
	parentTransport = tripperware.WrapForMapping(httpMapper, parentTransport)
	parentTransport = tripperware.WrapForRequestID("winch-", parentTransport)
	parentTransport = reverseProxyErrHandler(parentTransport, logEntry)

	bufferpool := bpool.NewBytePool(*flagBufferCount, *flagBufferSizeBytes)
	return &Proxy{
		mux: mux,
		kedgeReverseProxy: &httputil.ReverseProxy{
			Director:      func(r *http.Request) {},
			Transport:     parentTransport,
			FlushInterval: *flagFlushingInterval,
			BufferPool:    bufferpool,
			ErrorLog:      http_logrus.AsHttpLogger(logEntry.WithField("caller", "winch.ReverseProxy")),
			// Do not modify anything, just log interesting errors directly on winch.
			ModifyResponse: func(r *http.Response) error {
				if val := r.Header.Get(header.ResponseKedgeError); val != "" {
					// This request was not proxied. Why?
					tags := http_ctxtags.ExtractInbound(r.Request).Values()
					tags["err_type"] = r.Header.Get(header.ResponseKedgeErrorType)
					logEntry.WithFields(tags).WithError(errors.New(val)).Error("Kedge was unable to proxy request.")
				}
				return nil
			},
		},
		handleHTTPConnect: handleHTTPConnectFunc(httpMapper, grpcMapper, localGRPCPort),
	}
}

// Proxy is a forward/reverse proxy that implements Mapper+Kedge forwarding.
// Mux is for routes that are directed to winch directly (debug endpoints).
type Proxy struct {
	kedgeReverseProxy *httputil.ReverseProxy
	handleHTTPConnect http.HandlerFunc
	mux               *http.ServeMux
}

func (p *Proxy) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
	if _, ok := resp.(http.Flusher); !ok {
		panic("the http.ResponseWriter passed must be an http.Flusher")
	}

	if req.Method == http.MethodConnect {
		p.handleHTTPConnect(resp, req)
	}

	// Is this request directly to us, or just to be proxied?
	// This is to handle case when winch have /debug/xxx endpoint and we want to proxy through winch to same endpoint's path.
	if req.URL.Scheme == "" {
		p.mux.ServeHTTP(resp, req)
		return
	}
	normReq := proxyreq.NormalizeInboundRequest(req)
	p.kedgeReverseProxy.ServeHTTP(resp, normReq)
}

func handleHTTPConnectFunc(
	httpMapper winchMapper,
	grpcMapper winchMapper,
	localGRPCPort int,
) func(resp http.ResponseWriter, req *http.Request) {
	return func(resp http.ResponseWriter, req *http.Request) {
		destHostPort := req.Host
		_, err := grpcMapper.Map(req.URL.Hostname(), req.URL.Port())
		if err == nil {
			// It's kedge gRPC destination. Kedge does not support HTTP CONNECT on purpose.
			// We don't want to lose ability to add custom headers and drain proxy on rolling restart.
			// However if connection is insecure, we may want to do TLS termination here and just forward to local
			// gRPC endpoint here.
			destHostPort = fmt.Sprintf("localhost:%v", localGRPCPort)
		}
		if err != kedge_map.ErrNotKedgeDestination {
			http.Error(resp, err.Error(), http.StatusInternalServerError)
			return
		}

		_, err = httpMapper.Map(req.URL.Hostname(), req.URL.Port())
		if err == nil {
			// HTTP CONNECT for HTTPS is unavailable. Kedge does not support it and we cannot terminate TLS here, since
			// winch does not expose non-gRPC HTTP-TLS endpoint.
			http.Error(resp, "HTTP CONNECT for HTTPS routes are not implemented.", http.StatusNotImplemented)
			return
		}
		if err != kedge_map.ErrNotKedgeDestination {
			http.Error(resp, err.Error(), http.StatusInternalServerError)
			return
		}

		// We do TCP proxy for all not kedge destinations.
		destConn, err := net.DialTimeout("tcp", destHostPort, 10*time.Second)
		if err != nil {
			http.Error(resp, err.Error(), http.StatusServiceUnavailable)
			return
		}
		hijacker, ok := resp.(http.Hijacker)
		if !ok {
			http.Error(resp, "Hijacking is not supported", http.StatusInternalServerError)
			return

		}
		clientConn, _, err := hijacker.Hijack()
		if err != nil {
			http.Error(resp, err.Error(), http.StatusServiceUnavailable)
		}

		resp.WriteHeader(http.StatusOK)
		go transfer(destConn, clientConn)
		go transfer(clientConn, destConn)
	}
}
func transfer(destination io.WriteCloser, source io.ReadCloser) {
	defer destination.Close()
	defer source.Close()
	// TODO(bplotka): This seems unsafe -> we need to handle error
	io.Copy(destination, source)
}

func reverseProxyErrHandler(next http.RoundTripper, logEntry logrus.FieldLogger) http.RoundTripper {
	return httpwares.RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		t := reporter.Extract(req)
		resp, err := next.RoundTrip(req)
		if err != nil {
			t.ReportError(errtypes.TransportUnknownError, err)
			if resp == nil {
				resp = &http.Response{
					Request:    req,
					Header:     http.Header{},
					Body:       ioutil.NopCloser(&bytes.Buffer{}),
					StatusCode: http.StatusBadGateway,
				}
			}
			reporter.SetWinchErrorHeaders(resp.Header, t)
			// Mimick reverse proxy err handling.
			tags := http_ctxtags.ExtractInbound(req).Values()
			logEntry.WithFields(tags).WithError(err).Warn("HTTP roundTrip failed")
		}

		return resp, nil
	})
}
