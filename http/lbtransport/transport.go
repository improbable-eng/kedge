package lbtransport

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/jpillora/backoff"
	"github.com/mwitkow/go-httpwares/tags"
	"github.com/mwitkow/kedge/lib/http/ctxtags"
	"github.com/mwitkow/kedge/lib/reporter"
	"github.com/mwitkow/kedge/lib/reporter/errtypes"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/naming"
)

type tripper struct {
	targetName string

	parent           http.RoundTripper
	policy           LBPolicy
	lastResolveError error

	currentTargets []*Target
	mu             sync.RWMutex
	cancel         context.CancelFunc
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
func New(ctx context.Context, targetAddr string, parent http.RoundTripper, resolver naming.Resolver, policy LBPolicy) (*tripper, error) {
	innerCtx, cancel := context.WithCancel(ctx)
	s := &tripper{
		targetName:     targetAddr,
		parent:         parent,
		policy:         policy,
		currentTargets: []*Target{},
		cancel:         cancel,
	}

	go s.run(innerCtx, resolver, targetAddr)
	return s, nil
}

func (s *tripper) run(ctx context.Context, resolver naming.Resolver, targetAddr string) {
	resolveRetryBackoff := &backoff.Backoff{
		Min:    50 * time.Millisecond,
		Jitter: true,
		Factor: 2,
		Max:    2 * time.Second,
	}
	var lastNextError error

	for ctx.Err() == nil {
		watcher, err := resolver.Resolve(targetAddr)
		if err != nil {
			s.mu.Lock()
			s.currentTargets = []*Target{}
			// Don't loose lastNextError if there.
			if lastNextError != nil {
				err = errors.Wrap(lastNextError, err.Error())
			}
			s.lastResolveError = errors.Wrapf(err, "Failed to Resolver target %s. Retrying with backoff.", targetAddr)
			s.mu.Unlock()
			time.Sleep(resolveRetryBackoff.Duration())
			continue
		}
		resolveRetryBackoff.Reset()

		localCurrentTargets := []*Target{}
		// Starting getting Next updates. On Error we will retry.
		for ctx.Err() == nil {
			updates, err := watcher.Next() // blocking call until new updates are there
			if err != nil {
				lastNextError = err
				break // watcher.Errors are unrecoverable, so try to Resolve again from the beginning.
			}

			for _, u := range updates {
				if u.Op == naming.Add {
					localCurrentTargets = append(localCurrentTargets, &Target{DialAddr: u.Addr})
				} else if u.Op == naming.Delete {
					kept := []*Target{}
					for _, t := range localCurrentTargets {
						if u.Addr != t.DialAddr {
							kept = append(kept, t)
						}
					}
					localCurrentTargets = kept
				}
			}
			s.mu.Lock()
			s.currentTargets = localCurrentTargets
			s.mu.Unlock()
		}
		watcher.Close()
	}
}

// Close is only used in test. For proper closing we wait for context cancellation from parent context.
func (s *tripper) Close() error {
	s.cancel()
	return nil
}

func (s *tripper) RoundTrip(r *http.Request) (*http.Response, error) {
	tags := http_ctxtags.ExtractInbound(r)
	tags.Set(ctxtags.TagForBackendTarget, s.targetName)

	s.mu.RLock()
	targetsRef := s.currentTargets
	lastResolvErr := s.lastResolveError
	s.mu.RUnlock()
	if len(targetsRef) == 0 {
		return nil, reporter.WrapError(
			errtypes.NoResolutionAvailable,
			errors.Wrapf(lastResolvErr, "lb: no resolution available for %s", s.targetName),
		)
	}

	picker := s.policy.Picker()
	for {
		target, err := picker.Pick(r, targetsRef)
		if err != nil {
			return nil, reporter.WrapError(
				errtypes.NoConnToAllResolvedAddresses,
				errors.Wrapf(err, "lb: failed choosing valid target for %s", s.targetName),
			)
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
