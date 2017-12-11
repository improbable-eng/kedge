package matcher

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/improbable-eng/kedge/pkg/kedge/http/director/proxyreq"
	pb "github.com/improbable-eng/kedge/protogen/kedge/config/http/routes"
)

type Matcher struct {
	pbMatcher *pb.Matcher
}

func New(pbMatcher *pb.Matcher) *Matcher {
	return &Matcher{pbMatcher: pbMatcher}
}

func (m *Matcher) Match(req *http.Request) bool {
	if !urlMatches(req.URL, m.pbMatcher.PathRules) {
		return false
	}
	if !hostMatches(req.URL.Hostname(), m.pbMatcher.Host) {
		return false
	}
	if !portMatches(getPort(req), m.pbMatcher.Port) {
		return false
	}
	if !headersMatch(req.Header, m.pbMatcher.Header) {
		return false
	}
	if !requestTypeMatch(proxyreq.GetProxyMode(req), m.pbMatcher.ProxyMode) {
		return false
	}
	return true
}

func getPort(req *http.Request) string {
	port := req.URL.Port()
	if port == "" {
		switch strings.ToLower(req.URL.Scheme) {
		case "", "http":
			port = "80"
		case "https":
			port = "443"
		}
	}
	return port
}

func urlMatches(u *url.URL, matchers []string) bool {
	if len(matchers) == 0 {
		return true
	}
	for _, m := range matchers {
		if m == "" {
			continue
		}
		if m[len(m)-1] == '*' {
			if strings.HasPrefix(u.Path, m[0:len(m)-1]) {
				return true
			}
		}
		if m == u.Path {
			return true
		}
	}
	return false
}

func hostMatches(host string, matcher string) bool {
	if matcher == "" {
		return false // we can't handle empty hosts
	}
	if matcher == "" {
		return true // no matcher set, match all like a boss!
	}

	// TODO(bplotka): Change it to regexp.
	if matcher[0] != '*' {
		return host == matcher
	}
	return strings.HasSuffix(host, matcher[1:])
}

func portMatches(port string, matcher uint32) bool {
	if matcher == 0 {
		return true // no matcher set, match all like a boss!
	}

	if port == "" {
		return false // we expect certain port.
	}

	return port == fmt.Sprintf("%v", matcher)
}

func headersMatch(header http.Header, expectedKv map[string]string) bool {
	for expK, expV := range expectedKv {
		headerVal := header.Get(expK)
		if headerVal == "" {
			return false // key doesn't exist
		}
		if headerVal != expV {
			return false
		}
	}
	return true
}

func requestTypeMatch(requestMode proxyreq.ProxyMode, routeMode pb.ProxyMode) bool {
	if routeMode == pb.ProxyMode_ANY {
		return true
	}
	if requestMode == proxyreq.MODE_FORWARD_PROXY && routeMode == pb.ProxyMode_FORWARD_PROXY {
		return true
	}
	if requestMode == proxyreq.MODE_REVERSE_PROXY && routeMode == pb.ProxyMode_REVERSE_PROXY {
		return true
	}
	return false
}
