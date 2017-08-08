package winch

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	pb "github.com/mwitkow/kedge/_protogen/winch/config"
	"github.com/mwitkow/kedge/lib/auth"
	"github.com/mwitkow/kedge/lib/map"
)

type StaticRoutes struct {
	routes []kedge_map.RouteGetter
}

func NewStaticRoutes(factory *AuthFactory, mapperConfig *pb.MapperConfig, authConfig *pb.AuthConfig) (*StaticRoutes, error) {
	var routes []kedge_map.RouteGetter
	for _, configRoute := range mapperConfig.Routes {
		backendAuth, err := routeAuth(factory, authConfig, configRoute.BackendAuth)
		if err != nil {
			return nil, err
		}

		proxyAuth, err := routeAuth(factory, authConfig, configRoute.ProxyAuth)
		if err != nil {
			return nil, err
		}

		route := &kedge_map.Route{
			BackendAuth: backendAuth,
			ProxyAuth:   proxyAuth,
		}

		var routeMatcher kedge_map.RouteGetter
		if direct := configRoute.GetDirect(); direct != nil {
			routeMatcher, err = newDirect(direct, route)
			if err != nil {
				return nil, err
			}
		} else if re := configRoute.GetRegexp(); re != nil {
			routeMatcher, err = newRegexp(re, route)
			if err != nil {
				return nil, err
			}
		} else {
			return nil, errors.New("Config validation failed. No route rule (regexp|direct) configured")
		}

		routes = append(routes, routeMatcher)
	}

	return &StaticRoutes{
		routes: routes,
	}, nil
}

func (r *StaticRoutes) Get() []kedge_map.RouteGetter {
	return r.routes
}

func routeAuth(f *AuthFactory, authConfig *pb.AuthConfig, authName string) (auth.Source, error) {
	if authName == "" {
		return NoAuth, nil
	}

	for _, source := range authConfig.AuthSources {
		if source.Name == authName {
			auth, err := f.Get(source)
			if err != nil {
				return nil, err
			}
			return auth, nil
		}
	}
	return nil, fmt.Errorf("Config validation failed. Not found auth source called %q", authName)
}

type regexpRoute struct {
	baseRoute *kedge_map.Route

	re               *regexp.Regexp
	clusterGroupName string // optional.
	urlTemplated     string
}

func newRegexp(re *pb.RegexpRoute, route *kedge_map.Route) (kedge_map.RouteGetter, error) {
	reexp, err := regexp.Compile(re.Exp)
	if err != nil {
		return nil, err
	}

	return &regexpRoute{
		baseRoute: route,

		re:           reexp,
		urlTemplated: re.Url,
	}, nil
}

func (r *regexpRoute) Route(hostPort string) (*kedge_map.Route, bool, error) {
	if !r.re.MatchString(hostPort) {
		return nil, false, nil
	}

	routeURL := strings.TrimSpace(r.urlTemplated)
	match := r.re.FindStringSubmatch(hostPort)
	for i, name := range r.re.SubexpNames() {
		if i == 0 {
			continue
		}

		// We care only by named match groups.
		if name == "" {
			continue
		}

		routeURL = strings.Replace(routeURL, fmt.Sprintf("${%s}", name), match[i], -1)
	}

	u, err := url.Parse(routeURL)
	if err != nil {
		return nil, false, err
	}

	clonedRoute := *r.baseRoute
	clonedRoute.URL = u
	return &clonedRoute, true, nil
}

type directRoute struct {
	route *kedge_map.Route

	hostPort string
}

func newDirect(direct *pb.DirectRoute, route *kedge_map.Route) (kedge_map.RouteGetter, error) {
	parsed, err := url.Parse(direct.Url)
	if err != nil {
		return nil, err
	}

	route.URL = parsed
	return directRoute{
		route:    route,
		hostPort: direct.Key,
	}, nil
}

func (r directRoute) Route(hostPort string) (*kedge_map.Route, bool, error) {
	if r.hostPort != hostPort {
		return nil, false, nil
	}
	clonedRoute := &kedge_map.Route{}
	*clonedRoute = *r.route
	return clonedRoute, true, nil
}
