package lbtransport

import (
	"context"
	"fmt"
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

type LBPolicy interface {
	// Picker returns PolicyPicker that is suitable to be used within single request.
	Picker() LBPolicyPicker

	Close()
}

// LBPolicyPicker decides which target to pick for a given call. Should be short-living.
type LBPolicyPicker interface {
	// Pick decides on which target to use for the request out of the provided ones.
	Pick(req *http.Request, currentTargets []*Target) (*Target, error)
	// ExcludeHost excludes the given target for a short time. It is useful to report no connection (even temporary).
	ExcludeTarget(*Target)
}

// Target represents the canonical address of a backend.
type Target struct {
	DialAddr string
}

// roundRobinPolicy picks target using round robin behaviour.
// It does NOT dial to the chosen target to check if it is accessible, instead it exposes ExcludeTarget method that allows to report
// connection troubles. That handles the situation when DNS resolution contains invalid targets. In  that case, it
// blacklists it for defined period of time called "blacklist backoff".
type roundRobinPolicy struct {
	blacklistBackoffDuration time.Duration
	blacklistMu              sync.Mutex
	blacklistedTargets       map[Target]time.Time

	atomicCounter uint64

	dialTimeout time.Duration
	closeFn     context.CancelFunc

	// For testing purposes.
	timeNow func() time.Time
}

func RoundRobinPolicyFromFlags() LBPolicy {
	return RoundRobinPolicy(*flagBlacklistBackoff, *flagTrialDialTimeout)
}

func RoundRobinPolicy(backoffDuration time.Duration, dialTimeout time.Duration) LBPolicy {
	ctx, cancel := context.WithCancel(context.Background())
	rr := &roundRobinPolicy{
		blacklistBackoffDuration: backoffDuration,
		blacklistedTargets:       make(map[Target]time.Time),
		dialTimeout:              dialTimeout,
		closeFn:                  cancel,

		timeNow: time.Now,
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Minute):
			}
			rr.cleanUpBlacklist()
		}
	}()
	return rr
}

func (rr *roundRobinPolicy) Close() {
	rr.closeFn()
}

func (rr *roundRobinPolicy) cleanUpBlacklist() {
	rr.blacklistMu.Lock()
	defer rr.blacklistMu.Unlock()

	for target, failTime := range rr.blacklistedTargets {
		if failTime.Add(rr.blacklistBackoffDuration).Before(rr.timeNow()) {
			// Expired.
			delete(rr.blacklistedTargets, target)
		}
	}
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

func (rr *roundRobinPolicy) isBlacklistDisabled() bool {
	return rr.blacklistBackoffDuration == (0 * time.Microsecond)
}

func (rr *roundRobinPolicy) Picker() LBPolicyPicker {
	return &roundRobinPolicyPicker{
		base:           rr,
		localBlacklist: make(map[Target]struct{}),
	}
}

type roundRobinPolicyPicker struct {
	base *roundRobinPolicy

	// To make sure we only retry for valid targets.
	localBlacklist map[Target]struct{}
}

func (rr *roundRobinPolicyPicker) isTargetLocallyBlacklisted(target *Target) bool {
	_, ok := rr.localBlacklist[*target]
	return ok
}

func (rr *roundRobinPolicyPicker) Pick(r *http.Request, currentTargets []*Target) (*Target, error) {
	for range currentTargets {
		count := uint64(len(currentTargets))
		id := atomic.AddUint64(&(rr.base.atomicCounter), 1)
		targetId := int(id % count)
		target := currentTargets[targetId]

		if rr.isTargetLocallyBlacklisted(target) {
			// That target is blacklisted in local blacklist. Check another target.
			continue
		}

		if !rr.base.isBlacklistDisabled() && rr.base.isTargetBlacklisted(target) {
			// That target is blacklisted. Check another one.
			continue
		}

		return currentTargets[targetId], nil
	}
	return nil, fmt.Errorf("All targets %d are failing, try later.", len(currentTargets))
}

func (rr *roundRobinPolicyPicker) ExcludeTarget(target *Target) {
	rr.base.blacklistTarget(target)
	rr.localBlacklist[*target] = struct{}{}
}
