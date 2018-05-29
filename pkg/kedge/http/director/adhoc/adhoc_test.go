package adhoc

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/golang/protobuf/jsonpb"
	"github.com/improbable-eng/kedge/pkg/kedge/common"
	pb "github.com/improbable-eng/kedge/protogen/kedge/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdhocMatches(t *testing.T) {
	configJson := `
{ "adhoc_rules": [
	{
		"dnsNameMatcher": "*.somenamespace.svc.cluster.local",
		"port": {
			"default": 8080,
			"allowed": [8080, 8081],
			"allowed_ranges": [
				{
					"from": 11000,
					"to":   11200
				}
			]
		}
	},
	{
		"dnsNameMatcher": "*.pods.cluster.local",
		"port": {
			"default": 80,
			"allowed": [80],
			"allowed_ranges": [
				{
					"from": 10000,
					"to":   11200
				}
			]
		}
	},
	{
		"dnsNameMatcher": "*.pods.cluster.local.with.replace",
		"dnsNameReplace": {
			"pattern": "local.with.replace",
			"substitution": "replaced"
		},
		"port": {
			"default": 80,
			"allowed": [80]
		}
	},
	{
		"dnsNameMatcher": "*.misconfigured",
		"dnsNameReplace": {
			"pattern": "non-existing-pattern",
			"substitution": "should-not-replace"
		},
		"port": {
			"default": 80,
			"allowed": [80]
		}
	}
]}`
	config := &pb.DirectorConfig_Http{}
	require.NoError(t, jsonpb.UnmarshalString(configJson, config))

	oldLookup := common.DefaultALookup
	defer func() { common.DefaultALookup = oldLookup }()
	common.DefaultALookup = func(addr string) (names []string, err error) {
		fmt.Printf("adhoc_test.go:51 debugging %#v\n", addr)
		switch addr {
		case "1-2-3-4.namespace.pods.cluster.local":
			return []string{"1.2.3.4"}, nil
		case "somebackend.somenamespace.svc.cluster.local":
			return []string{"2.3.4.5", "2.3.4.6"}, nil
		case "weird.cluster.local":
			return []string{"7.6.5.4", "9.8.7.6"}, nil
		case "pod-0.namespace.pods.cluster.replaced":
			return []string{"1.2.3.10"}, nil
		default:
			return nil, errors.New("test lookup error")
		}
	}

	a := NewStaticAddresser(config.AdhocRules)

	for _, tcase := range []struct {
		name         string
		hostPort     string
		expectedAddr string
		expectedErr  string
	}{
		{
			name:         "matches default port",
			hostPort:     "1-2-3-4.namespace.pods.cluster.local",
			expectedAddr: "1.2.3.4:80",
		},
		{
			name:         "matches lower boundary of port range",
			hostPort:     "1-2-3-4.namespace.pods.cluster.local:11000",
			expectedAddr: "1.2.3.4:11000",
		},
		{
			name:         "matches upper boundary of port range",
			hostPort:     "1-2-3-4.namespace.pods.cluster.local:11200",
			expectedAddr: "1.2.3.4:11200",
		},
		{
			name:        "fails port check outside the boundary",
			hostPort:    "1-2-3-4.namespace.pods.cluster.local:11201",
			expectedErr: "adhoc: port 11201 is not allowed",
		},
		{
			name:         "matches non default allowed in list",
			hostPort:     "somebackend.somenamespace.svc.cluster.local:8081",
			expectedAddr: "2.3.4.5:8081",
		},
		{
			name:        "fails unmatched, even though addresses resolve",
			hostPort:    "weird.cluster.local:8081",
			expectedErr: "unknown route to service",
		},
		{
			name:        "fails dial errors",
			hostPort:    "otherbackend.somenamespace.svc.cluster.local:8081",
			expectedErr: "adhoc: cannot resolve otherbackend.somenamespace.svc.cluster.local:8081 host: test lookup error",
		},
		{
			name:         "matches and replaces host",
			hostPort:     "pod-0.namespace.pods.cluster.local.with.replace",
			expectedAddr: "1.2.3.10:80",
		},
		{
			name:        "fail not matched pattern",
			hostPort:    "pod-0.namespace.misconfigured",
			expectedErr: "adhoc: cannot resolve pod-0.namespace.misconfigured host: replace pattern non-existing-pattern does match given host pod-0.namespace.misconfigured. Configuration error",
		},
	} {
		t.Run(tcase.name, func(t *testing.T) {
			req, err := http.NewRequest("GET", "/foo", nil)
			require.NoError(t, err, "parsing the request shouldn't fail")
			req.URL.Host = tcase.hostPort
			be, err := a.Address(req.URL.Host)
			if tcase.expectedErr != "" {
				assert.EqualError(t, err, tcase.expectedErr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, be, tcase.expectedAddr, "must match expected address")
		})

	}
}
