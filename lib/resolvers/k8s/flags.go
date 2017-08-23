package k8sresolver

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net"
	"net/url"
	"os"

	"github.com/mwitkow/kedge/lib/sharedflags"
	"github.com/mwitkow/kedge/lib/tokenauth"
	"github.com/mwitkow/kedge/lib/tokenauth/sources/direct"
	"github.com/mwitkow/kedge/lib/tokenauth/sources/k8s"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/naming"
)

var (
	// NOTE: Default values for all flags are designed for running within k8s pod.
	defaultKubeURL = fmt.Sprintf("https://%s", net.JoinHostPort(os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")))
	fKubeAPIURL    = sharedflags.Set.String("k8sresolver_kubeapi_url", defaultKubeURL,
		"TCP address to Kube API server in a form of 'http(s)://host:value'. If empty it will be fetched from env variables:"+
			"KUBERNETES_SERVICE_HOST and KUBERNETES_SERVICE_PORT")
	fInsecureSkipVerify = sharedflags.Set.Bool("k8sresolver_tls_insecure", false, "If enabled, no server verification will be "+
		"performed on client side. Not recommended.")
	fKubeAPIRootCAPath = sharedflags.Set.String("k8sresolver_ca_file", defaultSACACert, "Path to service account CA file. "+
		"Required if kubeapi_tls_insecure = false.")

	// Different kinds of auth are supported. Currently supported with flags:
	// - specifying file with token
	// - specifying user (access) for kube config auth section to be reused
	fTokenAuthPath = sharedflags.Set.String("k8sresolver_token_file", defaultSAToken,
		"Path to service account token to be used. This auth method has priority 2.")
	fKubeConfigAuthUser = sharedflags.Set.String("k8sresolver_kubeconfig_user", "",
		"If user is specified resolver will try to fetch api auth method directly from kubeconfig. "+
			"This auth method has priority 1.")
	fKubeConfigAuthPath = sharedflags.Set.String("k8sresolver_kubeconfig_path", "", "Kube config path. "+
		"Only used when k8sresolver_kubeconfig_user is specified. If empty it will try default path.")
)

// NewFromFlags creates resolver from flag from k8sresolver.sharedflags.Set.
func NewFromFlags(logger logrus.FieldLogger) (naming.Resolver, error) {
	k8sURL := *fKubeAPIURL
	if k8sURL == "" || k8sURL == "https://:" {
		return nil, errors.Errorf(
			"k8sresolver: k8sresolver_kubeapi_url flag needs to be specified or " +
				"KUBERNETES_SERVICE_HOST and KUBERNETES_SERVICE_PORT must be defined")
	}

	_, err := url.Parse(k8sURL)
	if err != nil {
		return nil, errors.Wrapf(err, "k8sresolver: k8sresolver_kubeapi_url flag needs to be valid URL. Value %s ", k8sURL)
	}
	tlsConfig := &tls.Config{
		InsecureSkipVerify: *fInsecureSkipVerify,
	}
	if !*fInsecureSkipVerify {
		ca, err := ioutil.ReadFile(*fKubeAPIRootCAPath)
		if err != nil {
			return nil, errors.Wrapf(err, "k8sresolver: failed to parse RootCA from file %s", *fKubeAPIRootCAPath)
		}
		certPool := x509.NewCertPool()
		certPool.AppendCertsFromPEM(ca)
		tlsConfig = &tls.Config{
			MinVersion: tls.VersionTLS10,
			RootCAs:    certPool,
		}
	}

	var source tokenauth.Source

	// Try kubeconfig auth first.
	if user := *fKubeConfigAuthUser; user != "" {
		source, err = k8sauth.New("kube_api", *fKubeConfigAuthPath, user)
		if err != nil {
			return nil, errors.Wrap(err, "k8sresolver: failed to create k8sauth Source")
		}
	}

	if source == nil {
		// Try token auth as fallback.
		token, err := ioutil.ReadFile(*fTokenAuthPath)
		if err != nil {
			return nil, errors.Wrapf(err, "k8sresolver: failed to parse token from %s. No auth method found", *fTokenAuthPath)
		}
		source = directauth.New("kube_api", string(token))
	}

	return New(logger, k8sURL, source, tlsConfig), nil
}
