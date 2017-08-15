package lbtransport

import (
	"fmt"
	"net/http"
	"sync"

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
	targetRef := s.currentTargets
	lastResolvErr := s.lastResolveError
	s.mu.RUnlock()
	if len(targetRef) == 0 {
		return nil, fmt.Errorf("lb: no targets available, last resolve err: %v", lastResolvErr)
	}

	target, err := s.policy.Pick(r, targetRef)
	if err != nil {
		return nil, fmt.Errorf("lb: failed choosing target: %v", err)
	}

	// Override the host for downstream Tripper, usually http.DefaultTransport.
	// http.Default transport uses `URL.Host` for Dial(<host>) and relevant connection pooling.
	// We override it to make sure it enters the appropriate dial method and hte appropriate connection pool.
	// See http.connectMethodKey.
	r.URL.Host = target.DialAddr
	return s.parent.RoundTrip(r)
}
