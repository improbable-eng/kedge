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
	"github.com/sirupsen/logrus"
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
	resolvedAddrs = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "kedge",
		Name:      "k8sresolver_up_addresses",
		Help:      "Number of IPs that are correct from the point of view of this k8sresolver.",
	}, []string{"target"})

	watcherErrs = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "kedge",
		Name:      "k8sresolver_watcher_next_errors_total",
		Help:      "Count of all watcher next() irrecoverable errors.",
	}, []string{"target"})

	watcherGotChanges = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "kedge",
		Name:      "k8sresolver_watcher_next_got_changes_total",
		Help:      "Count of all changes that watcher got from streamer to update the addresses.",
	}, []string{"target"})
	prometheus.MustRegister(resolvedAddrs, watcherErrs, watcherGotChanges)
}

// resolver resolves service names using Kubernetes endpoints instead of usual SRV DNS lookup.
type resolver struct {
	cl     *client
	logger logrus.FieldLogger
}

func NewFromFlags(logger logrus.FieldLogger) (name naming.Resolver, err error) {
	apiClient, err := k8s.NewFromFlags()
	if err != nil {
		return nil, err
	}
	return NewWithClient(logger, apiClient), nil
}

// NewWithClient returns a new Kubernetes resolver using given k8s.APIClient configured to be used against kube-apiserver.
func NewWithClient(logger logrus.FieldLogger, apiClient *k8s.APIClient) naming.Resolver {
	return &resolver{
		cl: &client{
			k8sClient: apiClient,
		},
		logger: logger,
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

	target := targetEntry{
		namespace: "default",
		port:      noTargetPort,
	}

	// Check if the port is named port. If it is, strip it before verifying this is a valid URL. We do this because the
	// k8s resolver can work with named ports, but since go1.12 url.Parse does not support non-numeric ports.
	targetNameParts := strings.Split(targetName, ":")
	if len(targetNameParts) > 2 {
		return targetEntry{}, errors.Errorf("bad targetEntry - it contains multiple \":\" - expected format is %s", ExpectedTargetFmt)
	}
	if len(targetNameParts) == 2 {
		if p := targetNameParts[1]; p != "" {
			target.port = targetPort{
				value: p,
			}
			if _, err := strconv.Atoi(p); err != nil {
				// NaN
				target.port.isNamed = true
				// Remove the named port from the targetName since it is unsupported in url.Parse.
				targetName = targetNameParts[0]
			}
		}
	}

	// To be parsed in expected manner we need to add some schema. We validated if there is not any scheme above.
	u, err := url.Parse(fmt.Sprintf("no-matter://%s", targetName))
	if err != nil {
		return targetEntry{}, err
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
		r.logger,
		t,
		r.cl,
		resolvedAddrs.WithLabelValues(target),
		watcherErrs.WithLabelValues(target),
		watcherGotChanges.WithLabelValues(target),
	)
}
