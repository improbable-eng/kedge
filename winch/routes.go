package winch

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"text/template"

	pb "github.com/mwitkow/kedge/_protogen/winch/config"
	"github.com/mwitkow/kedge/lib/auth"
	"github.com/mwitkow/kedge/lib/map"
)

type StaticRoutes struct {
	routes []kedge_map.RouteMatcher
}

func NewStaticRoutes(mapperConfig *pb.MapperConfig, authConfig *pb.AuthConfig) (*StaticRoutes, error) {
	f := NewAuthFactory()

	var routes []kedge_map.RouteMatcher
	for _, configRoute := range mapperConfig.Routes {
		backendAuth, err := routeAuth(f, authConfig, configRoute.BackendAuth)
		if err != nil {
			return nil, err
		}

		proxyAuth, err := routeAuth(f, authConfig, configRoute.ProxyAuth)
		if err != nil {
			return nil, err
		}

		route := &kedge_map.Route{
			BackendAuth: backendAuth,
			ProxyAuth:   proxyAuth,
		}

		var routeMatcher kedge_map.RouteMatcher
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

func (r *StaticRoutes) Get() []kedge_map.RouteMatcher {
	return r.routes
}

func routeAuth(f *authFactory, authConfig *pb.AuthConfig, authName string) (auth.Source, error) {
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
	urlTmpl          *template.Template
}

func newRegexp(re *pb.RegexpRoute, route *kedge_map.Route) (kedge_map.RouteMatcher, error) {
	reexp, err := regexp.Compile(re.Exp)
	if err != nil {
		return nil, err
	}

	tmpl, err := template.New("").Parse(re.Url)
	if err != nil {
		return nil, err
	}

	return &regexpRoute{
		baseRoute: route,

		re:               reexp,
		clusterGroupName: re.ClusterGroupName,
		urlTmpl:          tmpl,
	}, nil
}

func (r *regexpRoute) Match(dns string) bool {
	return r.re.Match([]byte(dns))
}

func (r *regexpRoute) renderURL(cluster string) (*kedge_map.Route, error) {
	buf := &bytes.Buffer{}
	err := r.urlTmpl.Execute(buf, struct {
		Cluster string
	}{
		Cluster: cluster,
	})
	if err != nil {
		return nil, err
	}

	u, err := url.Parse(buf.String())
	if err != nil {
		return nil, err
	}

	route := &(*r.baseRoute)
	route.URL = u
	return route, nil
}

func (r *regexpRoute) Route(dns string) (*kedge_map.Route, error) {
	if r.clusterGroupName == "" {
		return r.renderURL("unknown")
	}

	match := r.re.FindStringSubmatch(dns)
	for i, name := range r.re.SubexpNames() {
		if r.clusterGroupName != name {
			continue
		}
		return r.renderURL(match[i])
	}
	return nil, fmt.Errorf("failed to found given named group %q inside regexp %q. Misconfiguration.",
		r.clusterGroupName, r.re.String())
}

type directRoute struct {
	route *kedge_map.Route

	dns string
}

func newDirect(direct *pb.DirectRoute, route *kedge_map.Route) (kedge_map.RouteMatcher, error) {
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

func (r directRoute) Match(dns string) bool {
	return r.dns == dns
}

func (r directRoute) Route(_ string) (*kedge_map.Route, error) {
	return &(*r.route), nil
}
