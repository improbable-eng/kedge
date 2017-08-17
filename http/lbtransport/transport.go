package lbtransport

import (
	"net"
	"net/http"
	"sync"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/naming"
)

type tripper struct {
	targetName string

	parent           http.RoundTripper
	watcher          naming.Watcher
	policy           LBPolicy
	lastResolveError error

	currentTargets []*Target
	close          chan struct{}
	mu             sync.RWMutex
}

var (
	failedDialsCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "kedge",
			Subsystem: "http_lbtransport",
			Name:      "failed_dials",
			Help:      "Total number of failed dials that are in resolver and should blacklist the target.",
		},
		[]string{"resolve_addr", "target"},
	)
)

func init() {
	prometheus.MustRegister(failedDialsCounter)
}

// New creates a new load-balanced Round Tripper for a single backend.
//
// This RoundTripper is meant to only dial a single backend, and will throw errors if the req.URL.Host
// doesn't match the targetAddr.
//
// For resolving backend addresses it uses a grpc.naming.Resolver, allowing for generic use.
func New(targetAddr string, parent http.RoundTripper, resolver naming.Resolver, policy LBPolicy) (*tripper, error) {
	s := &tripper{
		targetName:     targetAddr,
		parent:         parent,
		policy:         policy,
		currentTargets: []*Target{},
	}
	watcher, err := resolver.Resolve(targetAddr)
	if err != nil {
		return nil, err
	}
	s.watcher = watcher
	go s.run()
	return s, nil
}

func (s *tripper) run() {
	for {
		updates, err := s.watcher.Next() // blocking call until new updates are there
		if err != nil {
			s.mu.Lock()
			s.currentTargets = []*Target{}
			s.lastResolveError = err
			s.mu.Unlock()
			return // watcher.Next errors are irrecoverable.
		}
		s.mu.RLock()
		targets := s.currentTargets
		s.mu.RUnlock()
		for _, u := range updates {
			if u.Op == naming.Add {
				targets = append(targets, &Target{DialAddr: u.Addr})
			} else if u.Op == naming.Delete {
				kept := []*Target{}
				for _, t := range targets {
					if u.Addr != t.DialAddr {
						kept = append(kept, t)
					}
				}
				targets = kept
			}
		}
		s.mu.Lock()
		s.currentTargets = targets
		s.mu.Unlock()
	}
}

func (s *tripper) Close() error {
	s.watcher.Close()
	return nil
}

func (s *tripper) RoundTrip(r *http.Request) (*http.Response, error) {
	// TODO(mwitkow): Fixup this target name matching. Can we even do it??
	//if r.URL.Host != s.targetName {
	//	return nil, fmt.Errorf("lb: request Host '%v' doesn't match Target destination '%v'", r.Host, s.targetName)
	//}
	s.mu.RLock()
	targetsRef := s.currentTargets
	lastResolvErr := s.lastResolveError
	s.mu.RUnlock()
	if len(targetsRef) == 0 {
		return nil, errors.Wrap(lastResolvErr, "lb: no targets available")
	}

	picker := s.policy.Picker()
	for {
		target, err := picker.Pick(r, targetsRef)
		if err != nil {
			return nil, errors.Wrap(err, "lb: failed choosing target")
		}

		// Override the host for downstream Tripper, usually http.DefaultTransport.
		// http.Default transport uses `URL.Host` for Dial(<host>) and relevant connection pooling.
		// We override it to make sure it enters the appropriate dial method and the appropriate connection pool.
		// See http.connectMethodKey.
		r.URL.Host = target.DialAddr
		resp, err := s.parent.RoundTrip(r)
		if err == nil {
			return resp, nil
		}

		if !isDialError(err) {
			return resp, err
		}

		failedDialsCounter.WithLabelValues(s.targetName, target.DialAddr).Inc()

		// Retry without this target.
		// NOTE: We need to trust picker that it blacklist the targets well.
		picker.ExcludeTarget(target)
	}
}

func isDialError(err error) bool {
	if opErr, ok := err.(*net.OpError); ok {
		if opErr.Op == "dial" {
			return true
		}
	}
	return false
}
