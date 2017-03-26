package proxyreq

import (
	"context"
	"net/http"
)

type ProxyMode int

const (
	MODE_FORWARD_PROXY ProxyMode = iota
	MODE_REVERSE_PROXY
)

var (
	typeMarker = "proxy_mode_marker"
)

// NormalizeInboundRequest makes sure that the request received by the proxy has the destination inside URL.Host.
func NormalizeInboundRequest(r *http.Request) *http.Request {
	t := unnormalizedRequestMode(r)
	reqCopy := r.WithContext(context.WithValue(r.Context(), typeMarker, t))
	if t == MODE_REVERSE_PROXY {
		reqCopy.URL.Host = reqCopy.Host
	}
	return reqCopy
}

func unnormalizedRequestMode(r *http.Request) ProxyMode {
	if r.URL.Host != "" {
		// Forward Proxy requests embed the host information of the destination inside the RequestURI.
		return MODE_FORWARD_PROXY
	} else {
		return MODE_REVERSE_PROXY
	}

}

func GetProxyMode(r *http.Request) ProxyMode {
	v, ok := r.Context().Value(typeMarker).(ProxyMode)
	if ok {
		return v
	}
	return unnormalizedRequestMode(r)
}
