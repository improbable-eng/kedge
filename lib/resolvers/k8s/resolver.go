package k8sresolver

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	pb "github.com/mwitkow/kedge/_protogen/kedge/config/common/resolvers"
	"github.com/mwitkow/kedge/lib/tokenauth"
	"github.com/mwitkow/kedge/lib/tokenauth/http"
	"github.com/pkg/errors"
	"google.golang.org/grpc/naming"
)

const (
	defaultSAToken  = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	defaultSACACert = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"

	// ExpectedTargetFmt is an expected format of the targetEntry Name given to Resolver. This is complainant with
	// the kubeDNS/CoreDNS entry format.
	ExpectedTargetFmt = "<service>(|.<namespace>)(|.<whatever suffix>)(|:<port_name>|:<value number>)"
)

// resolver resolves service names using Kubernetes endpoints instead of usual SRV DNS lookup.
type resolver struct {
	cl *client
}

func NewFromConfig(conf *pb.K8SResolver) (target string, name naming.Resolver, err error) {
	resolver, err := NewFromFlags()
	if err != nil {
		return "", nil, err
	}
	return conf.GetDnsPortName(), resolver, nil
}

// New returns a new Kubernetes resolver with HTTP client (based on given tokenauth Source and tlsConfig) to be used against kube-apiserver.
func New(k8sURL string, source tokenauth.Source, tlsConfig *tls.Config) naming.Resolver {
	k8sClient := &http.Client{
		// TLS transport with auth injection.
		Transport: httpauth.NewTripper(
			&http.Transport{
				TLSClientConfig: tlsConfig,
			},
			source,
			"Authorization",
		),
	}
	return NewWithClient(k8sURL, k8sClient)
}

// NewWithClient returns a new Kubernetes resolver using given http.Client configured to be used against kube-apiserver.
func NewWithClient(k8sURL string, k8sClient *http.Client) naming.Resolver {
	return &resolver{
		cl: &client{
			k8sURL:    k8sURL,
			k8sClient: k8sClient,
		},
	}
}

type targetPort struct {
	isNamed bool
	value   string
}

var noTargetPort = targetPort{}

type targetEntry struct {
	service   string
	namespace string
	port      targetPort
}

// parseTarget understands 'ExpectedTargetFmt'.
func parseTarget(targetName string) (targetEntry, error) {
	if targetName == "" {
		return targetEntry{}, errors.New("Failed to parse targetEntry. Empty string")
	}

	if hasSchema(targetName) {
		return targetEntry{}, errors.Errorf("Bad targetEntry name. It cannot contain any schema. Expected format: %s", ExpectedTargetFmt)
	}

	// To be parsed in expected manner we need to add some schema. We validated if there is not any scheme above.
	u, err := url.Parse(fmt.Sprintf("no-matter://%s", targetName))
	if err != nil {
		return targetEntry{}, err
	}

	target := targetEntry{
		namespace: "default",
		port:      noTargetPort,
	}
	if p := u.Port(); p != "" {
		target.port = targetPort{
			value: p,
		}
		if _, err := strconv.Atoi(p); err != nil {
			// NaN
			target.port.isNamed = true
		}
	}

	serviceNamespace := strings.Split(u.Hostname(), ".")
	target.service = serviceNamespace[0]

	if len(serviceNamespace) >= 2 {
		target.namespace = serviceNamespace[1]
	}

	return target, nil
}

var schemaRegexp = regexp.MustCompile(`^[0-9a-z]*://.*`)

func hasSchema(targetName string) bool {
	return schemaRegexp.Match([]byte(targetName))
}

// Resolve creates a Kubernetes watcher for the targetEntry.
// It expects targetEntry in a form of usual k8s DNS entry. See const 'ExpectedTargetFmt'.
func (r *resolver) Resolve(target string) (naming.Watcher, error) {
	t, err := parseTarget(target)
	if err != nil {
		return nil, err
	}

	// Now the tricky part begins (:
	return startNewWatcher(t, r.cl)
}
