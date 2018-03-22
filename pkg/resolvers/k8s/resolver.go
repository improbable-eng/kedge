package k8sresolver

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/improbable-eng/kedge/pkg/k8s"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/naming"
)

const (
	// ExpectedTargetFmt is an expected format of the targetEntry Name given to Resolver. This is complainant with
	// the kubeDNS/CoreDNS entry format.
	ExpectedTargetFmt = "<service>(|.<namespace>)(|.<whatever suffix>)(|:<port_name>|:<value number>)"
)

var (
	resolvedAddrs     *prometheus.GaugeVec
	watcherErrs       *prometheus.CounterVec
	watcherGotChanges *prometheus.CounterVec
)

func init() {
	resolvedAddrs := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "kedge",
		Name:      "k8sresolver_up_addresses",
		Help:      "Number of IPs that are correct from the point of view of this k8sresolver.",
	}, []string{"target"})

	watcherErrs := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "kedge",
		Name:      "k8sresolver_watcher_next_errors_total",
		Help:      "Count of all watcher next() irrecoverable errors.",
	}, []string{"target"})

	watcherGotChanges := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "kedge",
		Name:      "k8sresolver_watcher_next_got_changes_total",
		Help:      "Count of all changes that watcher got from streamer to update the addresses.",
	}, []string{"target"})
	prometheus.MustRegister(resolvedAddrs, watcherErrs, watcherGotChanges)
}

// resolver resolves service names using Kubernetes endpoints instead of usual SRV DNS lookup.
type resolver struct {
	cl *client
}

func NewFromFlags() (name naming.Resolver, err error) {
	apiClient, err := k8s.NewFromFlags()
	if err != nil {
		return nil, err
	}
	return NewWithClient(apiClient), nil
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
	return startNewWatcher(
		t,
		r.cl,
		resolvedAddrs.WithLabelValues(target),
		watcherErrs.WithLabelValues(target),
		watcherGotChanges.WithLabelValues(target),
	)
}
