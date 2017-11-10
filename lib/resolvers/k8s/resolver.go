package k8sresolver

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	pb "github.com/improbable-eng/kedge/_protogen/kedge/config/common/resolvers"
	"github.com/improbable-eng/kedge/lib/k8s"
	"github.com/pkg/errors"
	"google.golang.org/grpc/naming"
)

const (
	// ExpectedTargetFmt is an expected format of the targetEntry Name given to Resolver. This is complainant with
	// the kubeDNS/CoreDNS entry format.
	ExpectedTargetFmt = "<service>(|.<namespace>)(|.<whatever suffix>)(|:<port_name>|:<value number>)"
)

// resolver resolves service names using Kubernetes endpoints instead of usual SRV DNS lookup.
type resolver struct {
	cl *client
}

func NewFromConfig(conf *pb.K8SResolver) (target string, name naming.Resolver, err error) {
	apiClient, err := k8s.NewFromFlags()
	if err != nil {
		return "", nil, err
	}
	return conf.GetDnsPortName(), NewWithClient(apiClient), nil
}

// NewWithClient returns a new Kubernetes resolver using given k8s.APIClient configured to be used against kube-apiserver.
func NewWithClient(apiClient *k8s.APIClient) naming.Resolver {
	return &resolver{
		cl: &client{
			k8sClient: apiClient,
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
