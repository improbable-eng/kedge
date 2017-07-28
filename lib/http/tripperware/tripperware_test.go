package tripperware

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/mwitkow/kedge/lib/auth"
	"github.com/mwitkow/kedge/lib/map"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testRoundTripper struct {
	t                             *testing.T
	expectedURL                   *url.URL
	expectedAuthValue             string
	expectedProxyAuthValue        string
	expectedRoute                 *kedge_map.Route
	expectedMissingMappingTripper bool
}

func (t *testRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	assert.Equal(t.t, t.expectedURL, req.URL)

	assert.Equal(t.t, t.expectedAuthValue, req.Header.Get(authHeader))
	assert.Equal(t.t, t.expectedProxyAuthValue, req.Header.Get(ProxyAuthHeader))

	r, ok, err := getRoute(req.Context())
	if t.expectedMissingMappingTripper {
		require.Error(t.t, err)
	} else if t.expectedRoute == nil {
		require.NoError(t.t, err)
		require.False(t.t, ok)
	} else {
		require.NoError(t.t, err)
		require.True(t.t, ok)
		assert.Equal(t.t, t.expectedRoute, r)
	}
	return nil, nil
}

func urlMustParse(t *testing.T, urlStr string) *url.URL {
	u, err := url.Parse(urlStr)
	require.NoError(t, err)
	return u
}

func testMapping(t *testing.T) map[string]*kedge_map.Route {
	return map[string]*kedge_map.Route{
		"resource.example.org": {
			URL: urlMustParse(t, "https://some-url1.example.com"),
			// No auth.
		},
		"resource-auth.example.org": {
			URL:         urlMustParse(t, "https://some-url2.example.com"),
			BackendAuth: auth.Dummy("auth1", "Bearer secret1"),
		},
		"resource-proxyauth.example.org": {
			URL:       urlMustParse(t, "https://some-url3.example.com"),
			ProxyAuth: auth.Dummy("proxy-auth2", "Bearer secret2"),
		},
		"resource-bothauths.example.org": {
			URL:         urlMustParse(t, "https://some-url4.example.com"),
			BackendAuth: auth.Dummy("auth3", "Bearer secret3"),
			ProxyAuth:   auth.Dummy("proxy-auth3", "Bearer secret4"),
		},
	}
}

func TestGetRoute(t *testing.T) {
	r := httptest.NewRequest("GET", "http://resource.example.org", nil)

	_, _, err := getRoute(r.Context())
	require.Error(t, err)

	r = requestWithRoute(r, nil)
	_, ok, err := getRoute(r.Context())
	require.NoError(t, err)
	assert.False(t, ok)

	testRoute := &kedge_map.Route{}
	r = requestWithRoute(r, testRoute)
	route, ok, err := getRoute(r.Context())
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, testRoute, route)
}

func TestMappingTripper(t *testing.T) {
	mapping := testMapping(t)

	dnsName := "resource-bothauths.example.org"
	parent := &testRoundTripper{
		t:             t,
		expectedURL:   urlMustParse(t, "http://"+dnsName),
		expectedRoute: mapping[dnsName],
		// Rest empty.
	}
	rt := WrapForMapping(kedge_map.Simple(mapping), parent)
	r := httptest.NewRequest("GET", "http://"+dnsName, nil)

	rt.RoundTrip(r)
}

func TestRoutingTripper_NoMappingTripper_Err(t *testing.T) {
	dnsName := "resource-bothauths.example.org"
	parent := &testRoundTripper{
		t:                             t,
		expectedURL:                   urlMustParse(t, "http://"+dnsName),
		expectedMissingMappingTripper: true,
		// Rest empty.
	}
	rt := WrapForRouting(parent)
	r := httptest.NewRequest("GET", "http://"+dnsName, nil)

	rt.RoundTrip(r)
}

func TestRoutingTripper_OK(t *testing.T) {
	mapping := testMapping(t)

	dnsName := "resource-bothauths.example.org"
	parent := &testRoundTripper{
		t:             t,
		expectedURL:   mapping[dnsName].URL,
		expectedRoute: mapping[dnsName],
		// Rest empty.
	}
	rt := WrapForMapping(kedge_map.Simple(mapping), WrapForRouting(parent))
	r := httptest.NewRequest("GET", "http://"+dnsName, nil)

	rt.RoundTrip(r)
}

func TestRoutingTripper_NotKedgeDestination(t *testing.T) {
	mapping := testMapping(t)

	dnsName := "resource-not-proxy.example.org"
	parent := &testRoundTripper{
		t:             t,
		expectedURL:   urlMustParse(t, "http://"+dnsName),
		expectedRoute: nil,
		// Rest empty.
	}
	rt := WrapForMapping(kedge_map.Simple(mapping), WrapForRouting(parent))
	r := httptest.NewRequest("GET", "http://"+dnsName, nil)

	rt.RoundTrip(r)
}

func TestAuthTripper_NoMappingTripper_Err(t *testing.T) {
	dnsName := "resource-bothauths.example.org"
	parent := &testRoundTripper{
		t:                             t,
		expectedURL:                   urlMustParse(t, "http://"+dnsName),
		expectedMissingMappingTripper: true,
		// Rest empty.
	}
	rt := WrapForBackendAuth(parent)
	r := httptest.NewRequest("GET", "http://"+dnsName, nil)

	rt.RoundTrip(r)
}

func TestBackendAuthTripper_OK(t *testing.T) {
	mapping := testMapping(t)

	dnsName := "resource-bothauths.example.org"
	a, err := mapping[dnsName].BackendAuth.HeaderValue()
	require.NoError(t, err)
	parent := &testRoundTripper{
		t:                 t,
		expectedURL:       urlMustParse(t, "http://"+dnsName),
		expectedAuthValue: a,
		expectedRoute:     mapping[dnsName],
		// Rest empty.
	}
	rt := WrapForMapping(kedge_map.Simple(mapping), WrapForBackendAuth(parent))
	r := httptest.NewRequest("GET", "http://"+dnsName, nil)

	rt.RoundTrip(r)
}

func TestProxyAuthTripper_OK(t *testing.T) {
	mapping := testMapping(t)

	dnsName := "resource-bothauths.example.org"
	a, err := mapping[dnsName].ProxyAuth.HeaderValue()
	require.NoError(t, err)
	parent := &testRoundTripper{
		t:                      t,
		expectedURL:            urlMustParse(t, "http://"+dnsName),
		expectedProxyAuthValue: a,
		expectedRoute:          mapping[dnsName],
		// Rest empty.
	}
	rt := WrapForMapping(kedge_map.Simple(mapping), WrapForProxyAuth(parent))
	r := httptest.NewRequest("GET", "http://"+dnsName, nil)

	rt.RoundTrip(r)
}

func TestAllTrippers_OK(t *testing.T) {
	mapping := testMapping(t)

	dnsName := "resource.example.org"
	parent := &testRoundTripper{
		t:             t,
		expectedURL:   mapping[dnsName].URL,
		expectedRoute: mapping[dnsName],
		// Rest empty.
	}
	rt := WrapForMapping(
		kedge_map.Simple(mapping),
		WrapForRouting(
			WrapForBackendAuth(
				WrapForProxyAuth(parent),
			),
		),
	)
	r := httptest.NewRequest("GET", "http://"+dnsName, nil)
	rt.RoundTrip(r)

	dnsName = "resource-auth.example.org"
	a, err := mapping[dnsName].BackendAuth.HeaderValue()
	require.NoError(t, err)
	parent = &testRoundTripper{
		t:             t,
		expectedURL:   mapping[dnsName].URL,
		expectedAuthValue: a,
		expectedRoute: mapping[dnsName],
		// Rest empty.
	}
	rt = WrapForMapping(
		kedge_map.Simple(mapping),
		WrapForRouting(
			WrapForBackendAuth(
				WrapForProxyAuth(parent),
			),
		),
	)
	r = httptest.NewRequest("GET", "http://"+dnsName, nil)
	rt.RoundTrip(r)

	dnsName = "resource-proxyauth.example.org"
	a, err = mapping[dnsName].ProxyAuth.HeaderValue()
	require.NoError(t, err)
	parent = &testRoundTripper{
		t:             t,
		expectedURL:   mapping[dnsName].URL,
		expectedProxyAuthValue: a,
		expectedRoute: mapping[dnsName],
		// Rest empty.
	}
	rt = WrapForMapping(
		kedge_map.Simple(mapping),
		WrapForRouting(
			WrapForBackendAuth(
				WrapForProxyAuth(parent),
			),
		),
	)
	r = httptest.NewRequest("GET", "http://"+dnsName, nil)
	rt.RoundTrip(r)

	dnsName = "resource-bothauths.example.org"
	a, err = mapping[dnsName].BackendAuth.HeaderValue()
	require.NoError(t, err)
	a2, err := mapping[dnsName].ProxyAuth.HeaderValue()
	require.NoError(t, err)
	parent = &testRoundTripper{
		t:             t,
		expectedURL:   mapping[dnsName].URL,
		expectedAuthValue: a,
		expectedProxyAuthValue: a2,
		expectedRoute: mapping[dnsName],
		// Rest empty.
	}
	rt = WrapForMapping(
		kedge_map.Simple(mapping),
		WrapForRouting(
			WrapForBackendAuth(
				WrapForProxyAuth(parent),
			),
		),
	)
	r = httptest.NewRequest("GET", "http://"+dnsName, nil)
	rt.RoundTrip(r)
}
