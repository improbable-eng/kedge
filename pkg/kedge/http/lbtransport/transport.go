package lbtransport

import (
	"context"
	"net"
	"net/http"
	"sync"

	"github.com/improbable-eng/kedge/pkg/http/tripperware"

	http_ctxtags "github.com/improbable-eng/go-httpwares/tags"
	"github.com/improbable-eng/kedge/pkg/http/ctxtags"
	"github.com/improbable-eng/kedge/pkg/reporter"
	"github.com/improbable-eng/kedge/pkg/reporter/errtypes"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/naming"
)

type tripper struct {
	targetName string

	parent http.RoundTripper
	policy LBPolicy

	mu               sync.RWMutex
	currentTargets   []*Target
	irrecoverableErr error
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
	s := &tripper{
		targetName:     targetAddr,
		parent:         parent,
		policy:         policy,
		currentTargets: []*Target{},
	}

	watcher, err := resolver.Resolve(targetAddr)
	if err != nil {
		return nil, errors.Wrapf(err, "tripper: failed to do initial resolve for target %s", targetAddr)
	}
	go func() {
		<-ctx.Done()
		watcher.Close()
	}()
	go s.run(ctx, watcher)
	return s, nil
}

func (s *tripper) run(ctx context.Context, watcher naming.Watcher) {
	var localCurrentTargets []*Target
	for ctx.Err() == nil {
		updates, err := watcher.Next() // blocking call until new updates are there
		if err != nil {
			// Watcher next errors are irrecoverable.
			s.mu.Lock()
			s.irrecoverableErr = err
			s.currentTargets = []*Target{}
			s.mu.Unlock()
			return
		}

		for _, u := range updates {
			if u.Op == naming.Add {
				localCurrentTargets = append(localCurrentTargets, &Target{DialAddr: u.Addr})
			} else if u.Op == naming.Delete {
				var kept []*Target
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
}

func (s *tripper) RoundTrip(r *http.Request) (*http.Response, error) {
	tags := http_ctxtags.ExtractInbound(r)
	tags.Set(ctxtags.TagForBackendTarget, s.targetName)

	s.mu.RLock()
	targetsRef := s.currentTargets
	irrecoverableErr := s.irrecoverableErr
	s.mu.RUnlock()
	if irrecoverableErr != nil {
		err := errors.Wrapf(irrecoverableErr, "lb: critical naming.Watcher error for target %s. Tripper is closed.", s.targetName)
		reporter.Extract(r).ReportError(errtypes.IrrecoverableWatcherError, err)
		_ = tripperware.Close(r.Body)
		return nil, err
	}

	if len(targetsRef) == 0 {
		err := errors.Errorf("lb: no backend is available for %s. 0 resolved addresses.", s.targetName)
		reporter.Extract(r).ReportError(errtypes.NoResolutionAvailable, err)
		_ = tripperware.Close(r.Body)
		return nil, err
	}

	if r.Body != nil {
		// We have to own true body for the request because we cannot reuse same reader closer
		// in multiple calls to http.Transport.
		body := r.Body
		defer func() { _ = tripperware.Close(body) }()
		r.Body = newLazyBufferedReader(body)
	}

	picker := s.policy.Picker()
	for {
		target, err := picker.Pick(r, targetsRef)
		if err != nil {
			err = errors.Wrapf(err, "lb: failed choosing valid target for %s", s.targetName)
			reporter.Extract(r).ReportError(errtypes.NoConnToAllResolvedAddresses, err)
			return nil, err
		}

		// Override the host for downstream Tripper, usually http.DefaultTransport.
		// http.Default transport uses `URL.Host` for Dial(<host>) and relevant connection pooling.
		// We override it to make sure it enters the appropriate dial method and the appropriate connection pool.
		// See http.connectMethodKey.
		r.URL.Host = target.DialAddr
		tags.Set(ctxtags.TagForTargetAddress, target.DialAddr)

		if r.Body != nil {
			r.Body.(*lazyBufferedReader).rewind()
		}

		resp, err := s.parent.RoundTrip(r)
		if err == nil {
			return resp, nil
		}

		if !isDialError(err) {
			reporter.Extract(r).ReportError(errtypes.TransportUnknownError, err)
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
