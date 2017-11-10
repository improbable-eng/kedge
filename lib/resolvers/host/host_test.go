package hostresolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testDomain = "domain.org"

func TestPortOverrideSRVResolver_Lookup(t *testing.T) {
	l := func(host string) (addrs []string, err error) {
		require.Equal(t, testDomain, host)
		return []string{
			"1.1.1.1",
			"1.1.1.2",
			"1.1.1.10",
		}, nil
	}

	p := newHostResolver(99, l)
	targets, err := p.Lookup(testDomain)
	require.NoError(t, err)

	assert.Equal(t, "1.1.1.1:99", targets[0].DialAddr)
	assert.Equal(t, "1.1.1.2:99", targets[1].DialAddr)
	assert.Equal(t, "1.1.1.10:99", targets[2].DialAddr)
}
