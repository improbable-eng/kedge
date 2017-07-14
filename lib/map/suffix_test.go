package kedge_map_test

import (
	"testing"

	"github.com/mwitkow/kedge/lib/map"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSuffix_MatchesOne(t *testing.T) {
	mapper, err := kedge_map.Suffix("*.clusters.local", ".example.com", "https")
	require.NoError(t, err, "no error on initialization")

	for _, tcase := range []struct {
		target      string
		kedgeUrl    string
		expectedErr error
	}{
		{
			target:      "myservice.mynamespace.us1.clusters.local",
			kedgeUrl:    "https://us1.example.com",
			expectedErr: nil,
		},
		{
			target:      "somethingmore.myservice.mynamespace.eu1.clusters.local",
			kedgeUrl:    "https://eu1.example.com",
			expectedErr: nil,
		},
		{
			target:      "something.google.com",
			kedgeUrl:    "",
			expectedErr: kedge_map.ErrNotKedgeDestination,
		},
		{
			target:      "another.local",
			kedgeUrl:    "",
			expectedErr: kedge_map.ErrNotKedgeDestination,
		},
	} {
		t.Run(tcase.target, func(t *testing.T) {
			route, err := mapper.Map(tcase.target)
			if tcase.expectedErr == nil {
				require.NoError(t, err, "no error")
				assert.Equal(t, tcase.kedgeUrl, route.URL.String(), "urls must match")
			} else {
				require.Equal(t, tcase.expectedErr, err, "error expected")
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
		expectedErr error
	}{
		{
			target:      "myservice.mynamespace.us1.prod.clusters.local",
			kedgeUrl:    "https://us1.prod.example.com",
			expectedErr: nil,
		},
		{
			target:      "somethingmore.myservice.mynamespace.eu1.staging.clusters.local",
			kedgeUrl:    "https://eu1.staging.example.com",
			expectedErr: nil,
		},
		{
			target:      "something.something.google.com",
			kedgeUrl:    "",
			expectedErr: kedge_map.ErrNotKedgeDestination,
		},
		{
			target:      "tooshort.clusters.local",
			kedgeUrl:    "",
			expectedErr: kedge_map.ErrNotKedgeDestination,
		},
	} {
		t.Run(tcase.target, func(t *testing.T) {
			route, err := mapper.Map(tcase.target)
			if tcase.expectedErr == nil {
				require.NoError(t, err, "no error")
				assert.Equal(t, tcase.kedgeUrl, route.URL.String(), "urls must match")
			} else {
				require.Equal(t, tcase.expectedErr, err, "error expected")
			}
		})
	}
}
