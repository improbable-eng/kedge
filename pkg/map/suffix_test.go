package kedge_map_test

import (
	"testing"

	kedge_map "github.com/improbable-eng/kedge/pkg/map"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSuffix_MatchesOne(t *testing.T) {
	mapper, err := kedge_map.Suffix("*.clusters.local", ".example.com", "https")
	require.NoError(t, err, "no error on initialization")

	for _, tcase := range []struct {
		target      string
		kedgeUrl    string
		expectedErr string
	}{
		{
			target:      "myservice.mynamespace.us1.clusters.local",
			kedgeUrl:    "https://us1.example.com",
			expectedErr: "",
		},
		{
			target:      "somethingmore.myservice.mynamespace.eu1.clusters.local",
			kedgeUrl:    "https://eu1.example.com",
			expectedErr: "",
		},
		{
			target:      "something.google.com",
			kedgeUrl:    "",
			expectedErr: "something.google.com is not a kedge destination",
		},
		{
			target:      "another.local",
			kedgeUrl:    "",
			expectedErr: "another.local is not a kedge destination",
		},
	} {
		t.Run(tcase.target, func(t *testing.T) {
			route, err := mapper.Map(tcase.target, "does not matter")
			if tcase.expectedErr == "" {
				require.NoError(t, err, "no error")
				assert.Equal(t, tcase.kedgeUrl, route.URL.String(), "urls must match")
			} else {
				require.Equal(t, tcase.expectedErr, err.Error(), "error expected")
			}
		})
	}
}

func TestSuffix_MatchesMulti(t *testing.T) {
	mapper, err := kedge_map.Suffix("*.*.clusters.local", ".example.com", "https")
	require.NoError(t, err, "no error on initialization")

	for _, tcase := range []struct {
		target      string
		kedgeUrl    string
		expectedErr string
	}{
		{
			target:      "myservice.mynamespace.us1.prod.clusters.local",
			kedgeUrl:    "https://us1.prod.example.com",
			expectedErr: "",
		},
		{
			target:      "somethingmore.myservice.mynamespace.eu1.staging.clusters.local",
			kedgeUrl:    "https://eu1.staging.example.com",
			expectedErr: "",
		},
		{
			target:      "something.something.google.com",
			kedgeUrl:    "",
			expectedErr: "something.something.google.com is not a kedge destination",
		},
		{
			target:      "tooshort.clusters.local",
			kedgeUrl:    "",
			expectedErr: "tooshort.clusters.local is not a kedge destination",
		},
	} {
		t.Run(tcase.target, func(t *testing.T) {
			route, err := mapper.Map(tcase.target, "does not matter")
			if tcase.expectedErr == "" {
				require.NoError(t, err, "no error")
				assert.Equal(t, tcase.kedgeUrl, route.URL.String(), "urls must match")
			} else {
				require.Equal(t, tcase.expectedErr, err.Error(), "error expected")
			}
		})
	}
}
