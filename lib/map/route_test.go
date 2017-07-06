package kedge_map_test

import (
	"errors"
	"net/url"
	"testing"

	"github.com/mwitkow/kedge/lib/map"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testRoute struct {
	dnsToMatch string
	returnURL  *url.URL
	returnErr  error
}

func (r testRoute) Match(dns string) bool {
	return r.dnsToMatch == dns
}

func (r testRoute) URL(_ string) (*url.URL, error) {
	return r.returnURL, r.returnErr
}

type testRoutes struct {
	routes []kedge_map.Route
}

func (t *testRoutes) Get() []kedge_map.Route {
	return t.routes
}

func TestRouteMapper(t *testing.T) {
	r := &testRoutes{
		routes: []kedge_map.Route{
			testRoute{
				dnsToMatch: "a.b.c",
				returnErr:  errors.New("some err"),
			},
			testRoute{
				dnsToMatch: "a.b.d",
				returnURL: &url.URL{
					Path: "some",
				},
			},
			// Same dns to match, but we need to test if the first will be taken.
			testRoute{
				dnsToMatch: "a.b.d",
				returnURL: &url.URL{
					Path: "some2",
				},
			},
			testRoute{
				dnsToMatch: "a.b.e",
				returnURL: &url.URL{
					Path: "some3",
				},
			},
		},
	}
	mapper := kedge_map.RouteMapper(r)

	for _, tcase := range []struct {
		target      string
		kedgeUrl    string
		expectedErr error
	}{
		{
			target:      "a.b.x",
			expectedErr: kedge_map.ErrNotKedgeDestination,
		},
		{
			target:      "a.b.d",
			kedgeUrl:    "some",
			expectedErr: nil,
		},
		{
			target:      "a.b.c",
			expectedErr: errors.New("some err"),
		},
		{
			target:   "a.b.e",
			kedgeUrl: "some3",
		},
	} {
		t.Run(tcase.target, func(t *testing.T) {
			u, err := mapper.Map(tcase.target)
			if tcase.expectedErr == nil {
				require.NoError(t, err, "no error")
				assert.Equal(t, tcase.kedgeUrl, u.Path, "urls must match")
			} else {
				require.Error(t, err, "error expected")
				require.Equal(t, tcase.expectedErr.Error(), err.Error())
			}
		})
	}
}
