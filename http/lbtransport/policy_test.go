package lbtransport

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	testFailBlacklistDuration = 2 * time.Second
	testOKDial                = func(_ context.Context, _ *Target) bool { return true }
)

func TestRoundRobinPolicy_PickWithBlacklist(t *testing.T) {
	req := httptest.NewRequest("GET", "http://127.0.0.1/x", nil)

	now := time.Now()
	rr := &roundRobinPolicy{
		blacklistBackoffDuration: testFailBlacklistDuration,
		blacklistedTargets:       make(map[Target]time.Time),
		tryDialFunc:              testOKDial,
		timeNow: func() time.Time {
			return now
		},
	}

	testTargets := []*Target{
		{
			DialAddr: "0",
		},
		{
			DialAddr: "1",
		},
		{
			DialAddr: "2",
		},
	}

	// Picking serially should give targets in exact order.
	target, err := rr.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[1], target)

	target, err = rr.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[2], target)

	target, err = rr.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[0], target)

	target, err = rr.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[1], target)

	rr.atomicCounter = 0
	rr.tryDialFunc = func(_ context.Context, target *Target) bool {
		if target == testTargets[0] {
			return false
		}
		return true
	}

	// With target nr 0 failing on dial, we should blacklist it and picking serially should give targets in exact order without that failing.
	target, err = rr.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[1], target)

	target, err = rr.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[2], target)

	target, err = rr.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[1], target)

	assert.True(t, rr.isTargetBlacklisted(testTargets[0]))

	rr.atomicCounter = 0
	rr.tryDialFunc = func(_ context.Context, target *Target) bool {
		if target == testTargets[1] {
			return false
		}
		return true
	}

	// With target nr 1 failing on dial, we should blacklist it and picking serially should give us the last working target.
	// Time not passed so even that we can dial to target nr 0 it should stay in blacklist.
	target, err = rr.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[2], target)

	target, err = rr.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[2], target)

	assert.True(t, rr.isTargetBlacklisted(testTargets[0]))
	assert.True(t, rr.isTargetBlacklisted(testTargets[1]))

	rr.tryDialFunc = func(_ context.Context, target *Target) bool {
		if target == testTargets[2] {
			return false
		}
		return true
	}

	_, err = rr.Pick(req, testTargets)
	require.Error(t, err, "all targets should be in blacklist")

	assert.True(t, rr.isTargetBlacklisted(testTargets[0]))
	assert.True(t, rr.isTargetBlacklisted(testTargets[1]))
	assert.True(t, rr.isTargetBlacklisted(testTargets[2]))

	rr.atomicCounter = 0
	// Let's imagine time passed, so all blacklisted guys should be fine now.
	rr.timeNow = func() time.Time {
		return now.Add(testFailBlacklistDuration).Add(10 * time.Millisecond)

	}

	// Still target nr 2 is failing on dial, but rest should be removed from blacklist and included in pick.
	target, err = rr.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[1], target)

	target, err = rr.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[0], target)

	target, err = rr.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[1], target)
}
