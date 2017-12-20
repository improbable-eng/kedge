package router

import (
	"testing"

	"github.com/golang/protobuf/jsonpb"
	"github.com/grpc-ecosystem/go-grpc-middleware/util/metautils"
	pb "github.com/improbable-eng/kedge/protogen/kedge/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
	"google.golang.org/grpc/metadata"
)

func TestRouteMatches(t *testing.T) {
	configJson := `
{ "routes": [
	{
		"backendName": "backendA",
		"serviceNameMatcher": "com.example.a.*"
	},
	{
		"backendName": "backendB_authorityA",
		"serviceNameMatcher": "com.*",
		"authorityHostMatcher": "authority_a.service.local"
	},
	{
		"backendName": "backendB_authorityB",
		"serviceNameMatcher": "*",
		"authorityHostMatcher": "authority_b.service.local"
	},
	{
		"backendName": "backendB_authorityC",
		"serviceNameMatcher": "*",
		"authorityHostMatcher": "authority_c.service.local",
		"authorityPortMatcher": 1234
	},
	{
		"backendName": "backendD",
		"serviceNameMatcher": "com.example.",
		"metadataMatcher": {
			"keyOne": "valueOne",
			"keyTwo": "valueTwo"
		}
	},
	{
		"backendName": "backendCatchAllCom",
		"serviceNameMatcher": "com.*"
	}
]}`
	config := &pb.DirectorConfig_Grpc{}
	require.NoError(t, jsonpb.UnmarshalString(configJson, config))
	r := &static{routes: config.Routes}

	for _, tcase := range []struct {
		name            string
		fullServiceName string
		md              metadata.MD
		expectedBackend string
		expectedErr     error
	}{
		{
			name:            "MatchesNoAuthorityJustService",
			fullServiceName: "com.example.a.MyService",
			md:              metadata.Pairs(),
			expectedBackend: "backendA",
		},
		{
			name:            "MatchesAuthorityAndService",
			fullServiceName: "com.example.blah.MyService",
			md:              metadata.Pairs(":authority", "authority_a.service.local"),
			expectedBackend: "backendB_authorityA",
		},
		{
			name:            "MatchesAuthorityAndServiceTakeTwo",
			fullServiceName: "something.else.MyService",
			md:              metadata.Pairs(":authority", "authority_b.service.local"),
			expectedBackend: "backendB_authorityB",
		},
		{
			name:            "MatchesAuthorityAndPort",
			fullServiceName: "something.else.MyService",
			md:              metadata.Pairs(":authority", "authority_c.service.local:1234"),
			expectedBackend: "backendB_authorityC",
		},
		{
			name:            "MatchesMatchesMetadata",
			fullServiceName: "com.example.whatever.MyService",
			md:              metadata.Pairs("keyOne", "valueOne", "keyTwo", "valueTwo", "keyThree", "somethingUnmatched"),
			expectedBackend: "backendCatchAllCom",
		},
		{
			name:            "MatchesFailsBackToCatchCom_OnBadMetadata",
			fullServiceName: "com.example.whatever.MyService",
			md:              metadata.Pairs("keyTwo", "valueTwo"),
			expectedBackend: "backendCatchAllCom",
		},
		{
			name:            "MatchesFailsBackToCatchCom_OnBadAuthority",
			fullServiceName: "com.example.blah.MyService",
			md:              metadata.Pairs(":authority", "authority_else.service.local"),
			expectedBackend: "backendCatchAllCom",
		},
		{
			name:            "MatchesFailsCompletely_NoBackend",
			fullServiceName: "noncom.else.MyService",
			md:              metadata.Pairs(":authority", "authority_else.service.local"),
			expectedErr:     routeNotFound,
		},
	} {
		t.Run(tcase.name, func(t *testing.T) {
			ctx := metautils.NiceMD(tcase.md).ToIncoming(context.TODO())
			be, err := r.Route(ctx, tcase.fullServiceName)
			if tcase.expectedErr != nil {
				assert.Equal(t, tcase.expectedErr, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tcase.expectedBackend, be, "must match expected backend")
		})

	}
}
