package winch

import (
	"bytes"
	"fmt"
	"net/url"
	"regexp"
	"text/template"

	pb "github.com/mwitkow/kedge/_protogen/winch/config"
	"github.com/mwitkow/kedge/lib/map"
)

type StaticRoutes struct {
	routes []kedge_map.Route
}

func NewStaticRoutes(config *pb.MapperConfig) (*StaticRoutes, error) {
	var routes []kedge_map.Route
	for _, route := range config.Routes {
		if direct := route.GetDirect(); direct != nil {
			d, err := newDirect(direct)
			if err != nil {
				return nil, err
			}
			routes = append(routes, d)
		}

		if re := route.GetRegexp(); re != nil {
			r, err := newRegexp(re)
			if err != nil {
				return nil, err
			}
			routes = append(routes, r)
		}
	}

	return &StaticRoutes{
		routes: routes,
	}, nil
}

func (r *StaticRoutes) Get() []kedge_map.Route {
	return r.routes
}

type regexpRoute struct {
	re               *regexp.Regexp
	clusterGroupName string // optional.
	urlTmpl          *template.Template
}

func newRegexp(re *pb.RegexpRoute) (kedge_map.Route, error) {
	reexp, err := regexp.Compile(re.Exp)
	if err != nil {
		return nil, err
	}

	tmpl, err := template.New("").Parse(re.KedgeUrl)
	if err != nil {
		return nil, err
	}

	return &regexpRoute{
		re:               reexp,
		clusterGroupName: re.ClusterGroupName,
		urlTmpl:          tmpl,
	}, nil
}

func (r *regexpRoute) Match(dns string) bool {
	return r.re.Match([]byte(dns))
}

func (r *regexpRoute) renderURL(cluster string) (*url.URL, error) {
	buf := &bytes.Buffer{}
	err := r.urlTmpl.Execute(buf, struct {
		Cluster string
	}{
		Cluster: cluster,
	})
	if err != nil {
		return nil, err
	}

	return url.Parse(buf.String())
}

func (r *regexpRoute) URL(dns string) (*url.URL, error) {
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
	dns string
	url *url.URL
}

func newDirect(direct *pb.DirectRoute) (kedge_map.Route, error) {
	parsed, err := url.Parse(direct.KedgeUrl)
	if err != nil {
		return nil, err
	}

	return directRoute{
		dns: direct.Key,
		url: parsed,
	}, nil
}

func (r directRoute) Match(dns string) bool {
	return r.dns == dns
}

func (r directRoute) URL(_ string) (*url.URL, error) {
	return r.url, nil
}
