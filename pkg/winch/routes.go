package winch

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	kedge_map "github.com/improbable-eng/kedge/pkg/map"
	"github.com/improbable-eng/kedge/pkg/tokenauth"
	pb "github.com/improbable-eng/kedge/protogen/winch/config"
	"github.com/pkg/errors"
)

type StaticRoutes struct {
	httpRoutes []kedge_map.RouteGetter
	gRPCRoutes []kedge_map.RouteGetter
}

func NewStaticRoutes(factory *AuthFactory, mapperConfig *pb.MapperConfig, authConfig *pb.AuthConfig) (*StaticRoutes, error) {
	var (
		httpRoutes []kedge_map.RouteGetter
		gRPCRoutes []kedge_map.RouteGetter
	)
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

		switch configRoute.Protocol {
		case pb.Protocol_ANY:
			httpRoutes = append(httpRoutes, routeMatcher)
			gRPCRoutes = append(gRPCRoutes, routeMatcher)
		case pb.Protocol_HTTP:
			httpRoutes = append(httpRoutes, routeMatcher)
		case pb.Protocol_GRPC:
			gRPCRoutes = append(gRPCRoutes, routeMatcher)
		default:
			return nil, errors.Errorf("Unknown protocol %s", configRoute.Protocol.String())
		}
	}

	return &StaticRoutes{
		httpRoutes: httpRoutes,
		gRPCRoutes: gRPCRoutes,
	}, nil
}

func (r *StaticRoutes) HTTP() []kedge_map.RouteGetter {
	return r.httpRoutes
}

func (r *StaticRoutes) GRPC() []kedge_map.RouteGetter {
	return r.gRPCRoutes
}

func routeAuth(f *AuthFactory, authConfig *pb.AuthConfig, authName string) (tokenauth.Source, error) {
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
	return nil, errors.Errorf("Config validation failed. Not found auth source called %q", authName)
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
		return nil, false, errors.Wrapf(err, "invalid evaluated URL in regexp winch route: %s", routeURL)
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
