package lbtransport

import (
	"net/http"
	"sync/atomic"
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

type simpleRoundRobinPolicy struct {
	atomicCounter uint64
}

func RoundRobinPolicy() LBPolicy {
	return &simpleRoundRobinPolicy{}
}

func (rr *simpleRoundRobinPolicy) Pick(req *http.Request, currentTargets []*Target) (*Target, error) {
	count := uint64(len(currentTargets))
	id := atomic.AddUint64(&(rr.atomicCounter), 1)
	targetId := int(id % count)
	return currentTargets[targetId], nil
}
