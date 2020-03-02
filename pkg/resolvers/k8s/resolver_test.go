package k8sresolver

import (
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTarget(t *testing.T) {
	for _, tcase := range []struct {
		target string

		expectgedTarget targetEntry
		expectedErr     error
	}{
		{
			target:      "",
			expectedErr: errors.New("Failed to parse targetEntry. Empty string"),
		},
		{
			target:      "http://service1",
			expectedErr: errors.Errorf("Bad targetEntry name. It cannot contain any schema. Expected format: %s", ExpectedTargetFmt),
		},
		{
			target:      "service1.ns1:123:123",
			expectedErr: errors.Errorf("bad targetEntry - it contains multiple \":\" - expected format is %s", ExpectedTargetFmt),
		},
		{
			target: "service1",
			expectgedTarget: targetEntry{
				service:   "service1",
				namespace: "default",
				port:      noTargetPort,
			},
		},
		{
			target: "service1.ns1",
			expectgedTarget: targetEntry{
				service:   "service1",
				namespace: "ns1",
				port:      noTargetPort,
			},
		},
		{
			// We allow that.
			target: "service2.ns2.svc.local",
			expectgedTarget: targetEntry{
				service:   "service2",
				namespace: "ns2",
				port:      noTargetPort,
			},
		},
		{
			// We allow that.
			target: "service3.ns3.svc.cluster1.example.org",
			expectgedTarget: targetEntry{
				service:   "service3",
				namespace: "ns3",
				port:      noTargetPort,
			},
		},
		{
			target: "service4.ns4:1010",
			expectgedTarget: targetEntry{
				service:   "service4",
				namespace: "ns4",
				port: targetPort{
					value:   "1010",
					isNamed: false,
				},
			},
		},
		{
			target: "service5.ns5:some-port",
			expectgedTarget: targetEntry{
				service:   "service5",
				namespace: "ns5",
				port: targetPort{
					value:   "some-port",
					isNamed: true,
				},
			},
		},
		{
			target: "service6.ns6:",
			expectgedTarget: targetEntry{
				service:   "service6",
				namespace: "ns6",
				port:      noTargetPort,
			},
		},
	} {
		t.Logf("Case %s", tcase.target)

		res, err := parseTarget(tcase.target)
		if tcase.expectedErr != nil {
			require.Error(t, err)
			assert.Equal(t, tcase.expectedErr.Error(), err.Error())
			continue
		}

		require.NoError(t, err)
		require.NotNil(t, res)
		assert.Equal(t, tcase.expectgedTarget, res)
	}
}
