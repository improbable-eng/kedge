package winch

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"

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

func (r *regexpRoute) Route(dns string) (*kedge_map.Route, bool, error) {
	if !r.re.MatchString(dns) {
		return nil, false, nil
	}

	u, err := url.Parse(r.re.ReplaceAllString(dns, r.urlTemplated))
	if err != nil {
		return nil, false, err
	}

	route := &(*r.baseRoute)
	route.URL = u
	return route, true, nil
}

type directRoute struct {
	route *kedge_map.Route

	dns string
}

func newDirect(direct *pb.DirectRoute, route *kedge_map.Route) (kedge_map.RouteGetter, error) {
	parsed, err := url.Parse(direct.Url)
	if err != nil {
		return nil, err
	}

	route.URL = parsed
	return directRoute{
		route: route,
		dns:   direct.Key,
	}, nil
}

func (r directRoute) Route(dns string) (*kedge_map.Route, bool, error) {
	if r.dns != dns {
		return nil, false, nil
	}
	return &(*r.route), true, nil
}
