package resolvers

import (
	"testing"
	"time"

	"github.com/improbable-eng/go-srvlb/srv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testDomain = "domain.org"

type testLookup struct {
	t *testing.T
}

func (l *testLookup) Lookup(domainName string) ([]*srv.Target, error) {
	require.Equal(l.t, testDomain, domainName)
	return []*srv.Target{
		{
			DialAddr: "1.1.1.1:80",
			Ttl:      10 * time.Second,
		},
		{
			DialAddr: "1.1.1.2",
			Ttl:      10 * time.Second,
		},
		{
			DialAddr: "1.1.1.10:81",
			Ttl:      10 * time.Second,
		},
	}, nil
}
func TestPortOverrideSRVResolver_Lookup(t *testing.T) {
	l := &testLookup{
		t: t,
	}

	p := newPortOverrideSRVResolver(99, l)
	targets, err := p.Lookup(testDomain)
	require.NoError(t, err)

	assert.Equal(t, "1.1.1.1:99", targets[0].DialAddr)
	assert.Equal(t, "1.1.1.2:99", targets[1].DialAddr)
	assert.Equal(t, "1.1.1.10:99", targets[2].DialAddr)
}
