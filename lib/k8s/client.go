package k8s

import (
	"net/http"
	"crypto/tls"
	"github.com/mwitkow/kedge/lib/tokenauth"
	"github.com/mwitkow/kedge/lib/tokenauth/http"
)

type APIClient struct {
	*http.Client

	Address string
}

// New returns a new Kubernetes client with HTTP client (based on given tokenauth Source and tlsConfig) to be used against kube-apiserver.
func New(k8sURL string, source tokenauth.Source, tlsConfig *tls.Config) *APIClient {
	return &APIClient{
		Client: &http.Client{
			// TLS transport with auth injection.
			Transport: httpauth.NewTripper(
				&http.Transport{
					TLSClientConfig: tlsConfig,
				},
				source,
				"Authorization",
			),
		},
		Address: k8sURL,
	}
}