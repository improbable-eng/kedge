package lbtransport

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	testFailBlacklistDuration = 2 * time.Second
)

func assertTargetsPickedInOrder(t *testing.T, picker LBPolicyPicker, testTargets []*Target, expectedTargets ...*Target) {
	req := httptest.NewRequest("GET", "http://does-not-matter", nil)

	// Picking serially should give targets in exact order.
	for _, expected := range expectedTargets {
		target, err := picker.Pick(req, testTargets)
		require.NoError(t, err)
		assert.Equal(t, expected, target)
	}
}

func TestRoundRobinPolicy_PickWithGlobalBlacklists(t *testing.T) {
	defer leaktest.CheckTimeout(t, 10*time.Second)()

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

	picker1 := rr.Picker()
	assertTargetsPickedInOrder(t, picker1, testTargets, testTargets[1], testTargets[2], testTargets[0], testTargets[1])

	rr.atomicCounter = 0
	picker1.ExcludeTarget(testTargets[0])
	assert.True(t, rr.isTargetBlacklisted(testTargets[0]))

	// To test global blacklist we need to spawn another picker.
	picker2 := rr.Picker()

	// With target number 0 in Global blacklist new picker should give targets in exact order without that failing one.
	assertTargetsPickedInOrder(t, picker2, testTargets, testTargets[1], testTargets[2], testTargets[1])

	rr.atomicCounter = 0
	picker2.ExcludeTarget(testTargets[1])
	assert.True(t, rr.isTargetBlacklisted(testTargets[0]))
	assert.True(t, rr.isTargetBlacklisted(testTargets[1]))

	// To test global blacklist we need to spawn another picker.
	picker3 := rr.Picker()

	// With target number 1 excluded as well, picking serially should give us the last working target.
	// Time not passed so nothing is included from last blacklist.
	assertTargetsPickedInOrder(t, picker3, testTargets, testTargets[2], testTargets[2], testTargets[2])

	picker3.ExcludeTarget(testTargets[2])
	assert.True(t, rr.isTargetBlacklisted(testTargets[0]))
	assert.True(t, rr.isTargetBlacklisted(testTargets[1]))
	assert.True(t, rr.isTargetBlacklisted(testTargets[2]))

	// To test global blacklist we need to spawn another picker.
	picker4 := rr.Picker()

	req := httptest.NewRequest("GET", "http://does-not-matter", nil)
	_, err := picker4.Pick(req, testTargets)
	require.Error(t, err, "all targets should be in blacklist")

	rr.atomicCounter = 0
	// Let's imagine time passed, so all blacklisted guys should be fine now.
	rr.timeNow = func() time.Time {
		return now.Add(testFailBlacklistDuration).Add(10 * time.Millisecond)
	}
	picker4.ExcludeTarget(testTargets[2])
	assert.True(t, rr.isTargetBlacklisted(testTargets[2]))

	// To test global blacklist we need to spawn another picker!
	picker5 := rr.Picker()

	// Still target number 2 is excluded, but rest should be removed from blacklist and included in pick.
	assertTargetsPickedInOrder(t, picker5, testTargets, testTargets[1], testTargets[0], testTargets[1])
}

func TestRoundRobinPolicy_PickWithLocalBlacklists(t *testing.T) {
	defer leaktest.CheckTimeout(t, 10*time.Second)()

	rr := &roundRobinPolicy{
		blacklistBackoffDuration: 0 * time.Millisecond, // No global blacklist!
		blacklistedTargets:       make(map[Target]time.Time),
		timeNow:                  time.Now,
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

	picker1 := rr.Picker()
	assertTargetsPickedInOrder(t, picker1, testTargets, testTargets[1], testTargets[2], testTargets[0], testTargets[1])

	rr.atomicCounter = 0
	picker1.ExcludeTarget(testTargets[0])
	assert.True(t, picker1.(*roundRobinPolicyPicker).isTargetLocallyBlacklisted(testTargets[0]))

	// With target number 0 in local blacklist new picker should give targets in exact order without that failing one.
	assertTargetsPickedInOrder(t, picker1, testTargets, testTargets[1], testTargets[2], testTargets[1])

	rr.atomicCounter = 0
	picker1.ExcludeTarget(testTargets[1])
	assert.True(t, picker1.(*roundRobinPolicyPicker).isTargetLocallyBlacklisted(testTargets[0]))
	assert.True(t, picker1.(*roundRobinPolicyPicker).isTargetLocallyBlacklisted(testTargets[1]))

	// With target number 1 excluded as well, picking serially should give us the last working target.
	// Time not passed so nothing is included from last blacklist.
	assertTargetsPickedInOrder(t, picker1, testTargets, testTargets[2], testTargets[2], testTargets[2])

	picker1.ExcludeTarget(testTargets[2])
	assert.True(t, picker1.(*roundRobinPolicyPicker).isTargetLocallyBlacklisted(testTargets[0]))
	assert.True(t, picker1.(*roundRobinPolicyPicker).isTargetLocallyBlacklisted(testTargets[1]))
	assert.True(t, picker1.(*roundRobinPolicyPicker).isTargetLocallyBlacklisted(testTargets[2]))

	req := httptest.NewRequest("GET", "http://does-not-matter", nil)
	_, err := picker1.Pick(req, testTargets)
	require.Error(t, err, "all targets should be in blacklist")
}

func TestRoundRobinPolicy_CleanupBlacklist(t *testing.T) {
	defer leaktest.CheckTimeout(t, 10*time.Second)()

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
