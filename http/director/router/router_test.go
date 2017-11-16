package router

import (
	"fmt"
	"net/http"
	"testing"

	pb_route "github.com/improbable-eng/kedge/protogen/kedge/config/http/routes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	routeConfigs = []*pb_route.Route{
		&pb_route.Route{
			BackendName: "a",
			HostMatcher: "nopath.example.com",
			PortMatcher: 80,
			ProxyMode:   pb_route.ProxyMode_REVERSE_PROXY,
		},
		&pb_route.Route{
			BackendName: "b",
			HostMatcher: "nopath.example.com",
			ProxyMode:   pb_route.ProxyMode_REVERSE_PROXY,
		},
		&pb_route.Route{
			BackendName: "c",
			HostMatcher: "nopath.port.example.com",
			PortMatcher: 8343,
			ProxyMode:   pb_route.ProxyMode_REVERSE_PROXY,
		},
		&pb_route.Route{
			BackendName: "d",
			PathRules:   []string{"/some/strict/path"},
			HostMatcher: "path.port.example.com",
			PortMatcher: 83,
			ProxyMode:   pb_route.ProxyMode_REVERSE_PROXY,
		},
		&pb_route.Route{
			BackendName: "e",
			PathRules:   []string{"/some/strict/path"},
			HostMatcher: "path.httsdefport.example.com",
			PortMatcher: 443,
			ProxyMode:   pb_route.ProxyMode_REVERSE_PROXY,
		},
	}
)

func TestRoute_NoPathNoPort_PortExpanded(t *testing.T) {
	r := NewStatic(routeConfigs)

	for _, tc := range []struct {
		host   string
		port   string
		scheme string
		path   string

		expectedBackend string
		expectedErr     error
	}{
		{
			// The default port should be appended and matched with 'a' backend route.
			scheme: "http",
			host:   "nopath.example.com",

			expectedBackend: "a",
		},
		{
			// The URL should be matched with 'a' backend route.
			scheme: "http",
			host:   "nopath.example.com",
			port:   "80",

			expectedBackend: "a",
		},
		{
			// Specific path should not change anything, since we don't match on it.
			scheme: "http",
			host:   "nopath.example.com",
			port:   "80",
			path:   "/test/path",

			expectedBackend: "a",
		},
		{
			// Different port should match into more port generic 'b' backend route.
			scheme: "http",
			host:   "nopath.example.com",
			port:   "83",

			expectedBackend: "b",
		},
		{
			// Specific host and port should be matched with 'c' backend route.
			scheme: "http",
			host:   "nopath.port.example.com",
			port:   "8343",

			expectedBackend: "c",
		},
		{
			// Specific host, port and path should be matched with 'd' backend route.
			scheme: "http",
			host:   "path.port.example.com",
			port:   "83",
			path: "/some/strict/path",

			expectedBackend: "d",
		},
		{
			// Specific host, port and path should be matched with 'e' backend route.
			scheme: "https",
			host:   "path.httsdefport.example.com",
			port:   "443",
			path: "/some/strict/path",

			expectedBackend: "e",
		},
		{
			// Same host, port and path should be matched with 'e' backend route as well, since
			// TLS default port will be appended and it exactly matches 'e' backend route.
			scheme: "https",
			host:   "path.httsdefport.example.com",
			path: "/some/strict/path",

			expectedBackend: "e",
		},
		{
			// Not matching host should be filtered out.
			scheme: "http",
			host:   "wrong.path.port.example.com",
			port:   "83",
			path: "/some/strict/path",

			expectedErr: ErrRouteNotFound,
		},
		{
			// Not matching port should be filtered out.
			scheme: "http",
			host:   "path.port.example.com",
			port:   "84",
			path: "/some/strict/path",

			expectedErr: ErrRouteNotFound,
		},
		{
			// Not matching path should be filtered out.
			scheme: "http",
			host:   "path.port.example.com",
			port:   "83",
			path: "/some/strict/pathwrong",

			expectedErr: ErrRouteNotFound,
		},
	} {
		url := fmt.Sprintf("%s://%s:%s%s",
			tc.scheme,
			tc.host,
			tc.port,
			tc.path,
		)
		req, err := http.NewRequest(http.MethodGet, url, nil)
		require.NoError(t, err)

		route, err := r.Route(req)
		if tc.expectedErr != nil {
			require.Equal(t, tc.expectedErr, err, url)
			continue
		}

		require.NoError(t, err, url)
		assert.Equal(t, tc.expectedBackend, route, url)
	}
}
