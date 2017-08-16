package lbtransport

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	testFailBlacklistDuration = 2 * time.Second
)

func TestRoundRobinPolicy_PickWithGlobalBlacklists(t *testing.T) {
	req := httptest.NewRequest("GET", "http://127.0.0.1/x", nil)

	now := time.Now()
	rr := &roundRobinPolicy{
		blacklistBackoffDuration: testFailBlacklistDuration,
		blacklistedTargets:       make(map[Target]time.Time),
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

	// Test Global blacklist.
	picker := rr.Picker()
	// Picking serially should give targets in exact order.
	target, err := picker.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[1], target)

	target, err = picker.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[2], target)

	target, err = picker.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[0], target)

	target, err = picker.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[1], target)

	rr.atomicCounter = 0
	picker.ExcludeTarget(testTargets[0])

	// To test global blacklist we need to spawn another picker!
	picker = rr.Picker()

	// With target nr 0 in Global blacklist, even new picker should give targets in exact order without that failing without the first one.
	target, err = picker.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[1], target)

	target, err = picker.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[2], target)

	target, err = picker.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[1], target)

	assert.True(t, rr.isTargetBlacklisted(testTargets[0]))

	rr.atomicCounter = 0
	picker.ExcludeTarget(testTargets[1])

	// To test global blacklist we need to spawn another picker!
	picker = rr.Picker()

	// With target nr 1 excluded, picking serially should give us the last working target.
	// Time not passed so even that we can dial to target nr 0 it should stay in blacklist.
	target, err = picker.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[2], target)

	target, err = picker.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[2], target)

	assert.True(t, rr.isTargetBlacklisted(testTargets[0]))
	assert.True(t, rr.isTargetBlacklisted(testTargets[1]))

	picker.ExcludeTarget(testTargets[2])
	// To test global blacklist we need to spawn another picker!
	picker = rr.Picker()

	_, err = picker.Pick(req, testTargets)
	require.Error(t, err, "all targets should be in blacklist")

	assert.True(t, rr.isTargetBlacklisted(testTargets[0]))
	assert.True(t, rr.isTargetBlacklisted(testTargets[1]))
	assert.True(t, rr.isTargetBlacklisted(testTargets[2]))

	rr.atomicCounter = 0
	// Let's imagine time passed, so all blacklisted guys should be fine now.
	rr.timeNow = func() time.Time {
		return now.Add(testFailBlacklistDuration).Add(10 * time.Millisecond)
	}
	picker.ExcludeTarget(testTargets[2])

	// To test global blacklist we need to spawn another picker!
	picker = rr.Picker()

	// Still target nr 2 is excluded, but rest should be removed from blacklist and included in pick.
	target, err = picker.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[1], target)

	target, err = picker.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[0], target)

	target, err = picker.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[1], target)
}

func TestRoundRobinPolicy_PickWithLocalBlacklists(t *testing.T) {
	req := httptest.NewRequest("GET", "http://127.0.0.1/x", nil)
	rr := &roundRobinPolicy{
		blacklistBackoffDuration: 0 * time.Millisecond, // No global blacklist!
		blacklistedTargets:       make(map[Target]time.Time),
		timeNow: time.Now,
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

	picker := rr.Picker()

	// Picking serially should give targets in exact order.
	target, err := picker.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[1], target)

	target, err = picker.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[2], target)

	target, err = picker.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[0], target)

	target, err = picker.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[1], target)

	rr.atomicCounter = 0
	picker.ExcludeTarget(testTargets[0])

	// With target nr 0 only in local blacklist, even new picker should give targets in exact order without that failing without the first one.
	target, err = picker.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[1], target)

	target, err = picker.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[2], target)

	target, err = picker.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[1], target)

	assert.True(t, picker.(*roundRobinPolicyPicker).isTargetLocallyBlacklisted(testTargets[0]))

	rr.atomicCounter = 0
	picker.ExcludeTarget(testTargets[1])

	// With target nr 1 excluded, picking serially should give us the last working target.
	target, err = picker.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[2], target)

	target, err = picker.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, testTargets[2], target)

	assert.True(t, picker.(*roundRobinPolicyPicker).isTargetLocallyBlacklisted(testTargets[0]))
	assert.True(t, picker.(*roundRobinPolicyPicker).isTargetLocallyBlacklisted(testTargets[1]))

	picker.ExcludeTarget(testTargets[2])

	_, err = picker.Pick(req, testTargets)
	require.Error(t, err, "all targets should be in blacklist")

	assert.True(t, picker.(*roundRobinPolicyPicker).isTargetLocallyBlacklisted(testTargets[0]))
	assert.True(t, picker.(*roundRobinPolicyPicker).isTargetLocallyBlacklisted(testTargets[1]))
	assert.True(t, picker.(*roundRobinPolicyPicker).isTargetLocallyBlacklisted(testTargets[2]))
}

func TestRoundRobinPolicy_CleanupBlacklist(t *testing.T) {
	req := httptest.NewRequest("GET", "http://127.0.0.1/x", nil)
	rr := &roundRobinPolicy{
		blacklistBackoffDuration: testFailBlacklistDuration,
		blacklistedTargets:       make(map[Target]time.Time),
	}

	now := time.Now()
	rr.timeNow = func() time.Time {
		return now
	}

	picker := rr.Picker()
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
	assert.Equal(t, 0, len(rr.blacklistedTargets), "at the beginning blacklist should be empty.")
	picker.ExcludeTarget(testTargets[1])
	_, err := picker.Pick(req, testTargets)
	require.NoError(t, err)
	assert.Equal(t, 1, len(rr.blacklistedTargets), "after one fail blacklist should include one target")
	rr.cleanUpBlacklist()
	assert.Equal(t, 1, len(rr.blacklistedTargets), "after cleanup report blacklist should still include one target, since time not passed")
	rr.timeNow = func() time.Time {
		return now.Add(testFailBlacklistDuration).Add(5 * time.Millisecond)
	}
	rr.cleanUpBlacklist()
	assert.Equal(t, 0, len(rr.blacklistedTargets), "after cleanup report blacklist should include zero targets, since failBlacklistDuration passed")
}
