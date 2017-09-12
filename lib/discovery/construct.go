package discovery

import (
	"sort"
	"strings"

	pb_config "github.com/mwitkow/kedge/_protogen/kedge/config"
	pb_resolvers "github.com/mwitkow/kedge/_protogen/kedge/config/common/resolvers"
	pb_grpcbackends "github.com/mwitkow/kedge/_protogen/kedge/config/grpc/backends"
	pb_grpcroutes "github.com/mwitkow/kedge/_protogen/kedge/config/grpc/routes"
	pb_httpbackends "github.com/mwitkow/kedge/_protogen/kedge/config/http/backends"
	pb_httproutes "github.com/mwitkow/kedge/_protogen/kedge/config/http/routes"
	"github.com/pkg/errors"
)

// lastSeenServicesToConfigs constructs director and backendpool configs from lastSeenServices and base configuration files.
// At the end it validates and sorts them.
func (u *updater) lastSeenServicesToConfigs() (*pb_config.DirectorConfig, *pb_config.BackendPoolConfig, error) {
	resultDirector, resultBackendpool := cloneBaseConfigs(u.baseDirectorConfig, u.baseBackendConfig)
	for _, serviceConf := range u.lastSeenServices {
		addRoutingsToDirector(resultDirector, serviceConf.routings)
		addBackendsToBackendpool(resultBackendpool, serviceConf.backends)
	}

	err := resultDirector.Validate()
	if err != nil {
		return nil, nil, errors.Wrap(err, "director config does not pass validation after generation.")
	}

	err = resultBackendpool.Validate()
	if err != nil {
		return nil, nil, errors.Wrap(err, "backendpool config does not pass validation after generation.")
	}

	// Sort it.
	httpDirectorRouteSort(resultDirector.GetHttp().Routes)
	grpcDirectorRouteSort(resultDirector.GetGrpc().Routes)
	httpBackendpoolSort(resultBackendpool.GetHttp().Backends)
	grpcBackendpoolSort(resultBackendpool.GetGrpc().Backends)

	return resultDirector, resultBackendpool, nil
}

func cloneBaseConfigs(baseDirector *pb_config.DirectorConfig, baseBackendpool *pb_config.BackendPoolConfig) (*pb_config.DirectorConfig, *pb_config.BackendPoolConfig) {
	resultDirectorConfig := &pb_config.DirectorConfig{
		Grpc: &pb_config.DirectorConfig_Grpc{},
		Http: &pb_config.DirectorConfig_Http{},
	}
	resultBackendPool := &pb_config.BackendPoolConfig{
		Grpc:             &pb_config.BackendPoolConfig_Grpc{},
		Http:             &pb_config.BackendPoolConfig_Http{},
		TlsServerConfigs: baseBackendpool.TlsServerConfigs,
	}

	// Copy base for HTTP.
	if baseDirector.GetHttp() != nil {
		for _, route := range baseDirector.GetHttp().GetRoutes() {
			resultDirectorConfig.GetHttp().Routes = append(resultDirectorConfig.GetHttp().Routes, route)
		}

		for _, route := range baseDirector.GetHttp().GetAdhocRules() {
			resultDirectorConfig.GetHttp().AdhocRules = append(resultDirectorConfig.GetHttp().AdhocRules, route)
		}
	}
	if baseBackendpool.GetHttp() != nil {
		for _, backend := range baseBackendpool.GetHttp().GetBackends() {
			resultBackendPool.GetHttp().Backends = append(resultBackendPool.GetHttp().Backends, backend)
		}
	}

	// Copy base for gRPC.
	if baseDirector.GetGrpc() != nil {
		for _, route := range baseDirector.GetGrpc().GetRoutes() {
			resultDirectorConfig.GetGrpc().Routes = append(resultDirectorConfig.GetGrpc().Routes, route)
		}
	}
	if baseBackendpool.GetGrpc() != nil {
		for _, backend := range baseBackendpool.GetGrpc().GetBackends() {
			resultBackendPool.GetGrpc().Backends = append(resultBackendPool.GetGrpc().Backends, backend)
		}
	}
	return resultDirectorConfig, resultBackendPool
}

func httpDirectorRouteSort(routes []*pb_httproutes.Route) {
	sort.Slice(routes, func(i int, j int) bool {
		firstRoute := routes[i]
		secondRoute := routes[j]

		if firstRoute.HostMatcher == secondRoute.HostMatcher {
			// This is critical. If they both share one host matcher and one does not have portMatcher, the latter needs to be
			// at the end.
			if firstRoute.PortMatcher == 0 {
				return false
			}

			if secondRoute.PortMatcher == 0 {
				return true
			}

			// Otherwise just sort based on port.
			return firstRoute.PortMatcher < secondRoute.PortMatcher
		}

		return strings.Compare(firstRoute.BackendName, secondRoute.BackendName) <= 0
	})
}

func grpcDirectorRouteSort(routes []*pb_grpcroutes.Route) {
	sort.Slice(routes, func(i int, j int) bool {
		firstRoute := routes[i]
		secondRoute := routes[j]

		if firstRoute.ServiceNameMatcher == secondRoute.ServiceNameMatcher {
			// This is critical. If they both share one host matcher and one does not have portMatcher, the latter needs to be
			// at the end.
			if firstRoute.PortMatcher == 0 {
				return false
			}

			if secondRoute.PortMatcher == 0 {
				return true
			}

			// Otherwise just sort based on port.
			return firstRoute.PortMatcher < secondRoute.PortMatcher
		}

		// TODO(bplotka): Add sorting based on globbing expression to not hide each one out.
		/// service_name_matcher is a globbing expression that matches a full gRPC service name.
		/// For example a method call to 'com.example.MyService/Create' would be matched by:
		///  - com.example.MyService
		///  - com.example.*
		///  - com.*
		///  - *
		/// If not present, '*' is default.
		return strings.Compare(firstRoute.BackendName, secondRoute.BackendName) <= 0
	})
}

func httpBackendpoolSort(backends []*pb_httpbackends.Backend) {
	sort.Slice(backends, func(i int, j int) bool {
		return strings.Compare(backends[i].Name, backends[j].Name) <= 0
	})
}

func grpcBackendpoolSort(backends []*pb_grpcbackends.Backend) {
	sort.Slice(backends, func(i int, j int) bool {
		return strings.Compare(backends[i].Name, backends[j].Name) <= 0
	})
}

func addRoutingsToDirector(director *pb_config.DirectorConfig, routings serviceRoutings) {
	for backendName, httpRoutes := range routings.http {
		for _, httpRoute := range httpRoutes {
			director.GetHttp().Routes = append(
				director.GetHttp().Routes,
				&pb_httproutes.Route{
					Autogenerated: true,
					BackendName:   backendName.String(),
					HostMatcher:   httpRoute.nameMatcher,
					PortMatcher:   httpRoute.portMatcher,
					ProxyMode:     pb_httproutes.ProxyMode_REVERSE_PROXY,
				},
			)
		}
	}

	for backendName, grpcRoutes := range routings.grpc {
		for _, grpcRoute := range grpcRoutes {
			director.GetGrpc().Routes = append(
				director.GetGrpc().Routes,
				&pb_grpcroutes.Route{
					Autogenerated:      true,
					BackendName:        backendName.String(),
					ServiceNameMatcher: grpcRoute.nameMatcher,
					PortMatcher:        grpcRoute.portMatcher,
				},
			)
		}
	}
}

func addBackendsToBackendpool(backendpool *pb_config.BackendPoolConfig, backends serviceBackends) {
	for backendName, domainPort := range backends.httpDomainPorts {
		backendpool.GetHttp().Backends = append(
			backendpool.GetHttp().Backends,
			&pb_httpbackends.Backend{
				Autogenerated: true,
				Name:          backendName.String(),
				Resolver: &pb_httpbackends.Backend_K8S{
					K8S: &pb_resolvers.K8SResolver{
						DnsPortName: domainPort,
					},
				},
				Balancer: pb_httpbackends.Balancer_ROUND_ROBIN,
			},
		)
	}

	for backendName, domainPort := range backends.grpcDomainPorts {
		backendpool.GetGrpc().Backends = append(
			backendpool.GetGrpc().Backends,
			&pb_grpcbackends.Backend{
				Autogenerated: true,
				Name:          backendName.String(),
				Resolver: &pb_grpcbackends.Backend_K8S{
					K8S: &pb_resolvers.K8SResolver{
						DnsPortName: domainPort,
					},
				},
				Balancer: pb_grpcbackends.Balancer_ROUND_ROBIN,
			},
		)
	}

}
