package lbtransport

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mwitkow/kedge/lib/sharedflags"
)

var (
	flagBlacklistBackoff = sharedflags.Set.Duration("lbtransport_failed_target_backoff_duration", 2*time.Second,
		"Duration to keep failed targets in blacklist. Set 0 to disable checking & blacklisting targets.")
	flagTrialDialTimeout = sharedflags.Set.Duration("lbtransport_trial_dial_timeout", 500*time.Millisecond,
		"Timeout for trial dialing if enabled.")
)

// LBPolicy decides which target to pick for a given call.
type LBPolicy interface {
	// Pick decides on which target to use for the request out of the provided ones.
	Pick(req *http.Request, currentTargets []*Target) (*Target, error)
}

// Target represents the canonical address of a backend.
type Target struct {
	DialAddr string
}

// roundRobinPolicy picks target using round robin behaviour.
// It also dials to the chosen target to check if it is accessible. That handles the situation when DNS
// resolution contains invalid targets. If the target is not accessible, it blacklists it for defined period of time called
// "blacklist backoff".
type roundRobinPolicy struct {
	blacklistBackoffDuration time.Duration
	blacklistMu              sync.Mutex
	blacklistedTargets       map[Target]time.Time

	atomicCounter uint64

	dialTimeout time.Duration
	tryDialFunc func(ctx context.Context, target *Target) bool

	timeNow func() time.Time
}

func RoundRobinPolicyFromFlags() LBPolicy {
	return RoundRobinPolicy(*flagBlacklistBackoff, *flagTrialDialTimeout)
}

func RoundRobinPolicy(backoffDuration time.Duration, dialTimeout time.Duration) LBPolicy {
	rr := &roundRobinPolicy{
		blacklistBackoffDuration: backoffDuration,
		blacklistedTargets:       make(map[Target]time.Time),
		dialTimeout:              dialTimeout,

		tryDialFunc: tryDial,
		timeNow:     time.Now,
	}
	return rr
}

func (rr *roundRobinPolicy) isTargetBlacklisted(target *Target) bool {
	rr.blacklistMu.Lock()
	defer rr.blacklistMu.Unlock()

	failTime, ok := rr.blacklistedTargets[*target]
	if !ok {
		return false
	}

	// It is blacklisted, but check if still valid.
	if failTime.Add(rr.blacklistBackoffDuration).Before(rr.timeNow()) {
		// Expired.
		delete(rr.blacklistedTargets, *target)
		return false
	}

	return true
}

func (rr *roundRobinPolicy) blacklistTarget(target *Target) {
	rr.blacklistMu.Lock()
	defer rr.blacklistMu.Unlock()

	rr.blacklistedTargets[*target] = rr.timeNow()
}

func (rr *roundRobinPolicy) isTrialDialingDisabled() bool {
	return rr.blacklistBackoffDuration == (0 * time.Microsecond)
}

func (rr *roundRobinPolicy) Pick(r *http.Request, currentTargets []*Target) (*Target, error) {
	for range currentTargets {
		count := uint64(len(currentTargets))
		id := atomic.AddUint64(&(rr.atomicCounter), 1)
		targetId := int(id % count)
		target := currentTargets[targetId]

		if rr.isTrialDialingDisabled() {
			return currentTargets[targetId], nil
		}

		if rr.isTargetBlacklisted(target) {
			// That target is blacklisted. Check another one.
			continue
		}

		// Target not blacklisted before, try it first.
		dialCtx, cancel := context.WithTimeout(r.Context(), rr.dialTimeout)
		if rr.tryDialFunc(dialCtx, target) {
			cancel()
			return currentTargets[targetId], nil
		}
		cancel()
		rr.blacklistTarget(target)
	}
	return nil, fmt.Errorf("All targets %d are failing, try later.", len(currentTargets))
}

func tryDial(ctx context.Context, target *Target) bool {
	var dialTimeout time.Duration
	if deadline, ok := ctx.Deadline(); ok {
		dialTimeout = deadline.Sub(time.Now())
	}
	// Try to do quick dial to check if the target is listening.
	conn, err := net.DialTimeout("tcp", target.DialAddr, dialTimeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
