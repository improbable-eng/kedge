package discovery

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	pb_config "github.com/mwitkow/kedge/_protogen/kedge/config"
	pb_resolvers "github.com/mwitkow/kedge/_protogen/kedge/config/common/resolvers"
	pb_grpcbackends "github.com/mwitkow/kedge/_protogen/kedge/config/grpc/backends"
	pb_grpcroutes "github.com/mwitkow/kedge/_protogen/kedge/config/grpc/routes"
	pb_httpbackends "github.com/mwitkow/kedge/_protogen/kedge/config/http/backends"
	pb_httproutes "github.com/mwitkow/kedge/_protogen/kedge/config/http/routes"
	"github.com/pkg/errors"
)

type httpRoute struct {
	hostMatcher string
	portMatcher uint32
	domainPort  string
}

type grpcRoute struct {
	serviceNameMatcher string
	portMatcher        uint32
	domainPort         string
}

type serviceKey struct {
	name, namespace string
}

type serviceRoutings struct {
	// by backend name.
	http map[string]httpRoute
	grpc map[string]grpcRoute
}

type updater struct {
	currentDirectorConfig *pb_config.DirectorConfig
	currentBackendConfig  *pb_config.BackendPoolConfig
	externalDomainSuffix  string
	httpAnnotationPrefix  string
	grpcAnnotationPrefix  string

	lastSeenServices map[serviceKey]serviceRoutings
}

func newUpdater(
	baseDirector *pb_config.DirectorConfig,
	baseBackendpool *pb_config.BackendPoolConfig,
	externalDomainSuffix string,
	httpAnnotationPrefix string,
	grpcAnnotationPrefix string,
) *updater {

	resultDirectorConfig, resultBackendPool := cloneConfigs(baseDirector, baseBackendpool)
	return &updater{
		currentDirectorConfig: resultDirectorConfig,
		currentBackendConfig:  resultBackendPool,
		externalDomainSuffix:  externalDomainSuffix,
		httpAnnotationPrefix:  httpAnnotationPrefix,
		grpcAnnotationPrefix:  grpcAnnotationPrefix,
		lastSeenServices:      make(map[serviceKey]serviceRoutings),
	}
}

func cloneConfigs(baseDirector *pb_config.DirectorConfig, baseBackendpool *pb_config.BackendPoolConfig) (*pb_config.DirectorConfig, *pb_config.BackendPoolConfig) {
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

func (u *updater) onEvent(e event) (*pb_config.DirectorConfig, *pb_config.BackendPoolConfig, error) {
	serviceObj := e.Object
	service := serviceKey{serviceObj.Metadata.Name, serviceObj.Metadata.Namespace}

	switch e.Type {
	case deleted:
		return u.onDeletedEvent(serviceObj, service)
	case added, modified:
		return u.onModifiedOrAddedEvent(serviceObj, service, e.Type)
	}
	return nil, nil, errors.Errorf("Got not supported event type %s", e.Type)
}

func (u *updater) onDeletedEvent(serviceObj service, service serviceKey) (*pb_config.DirectorConfig, *pb_config.BackendPoolConfig, error) {
	routings, ok := u.lastSeenServices[service]
	if !ok {
		return nil, nil, errors.Errorf("Got %s event for item %v that we are seeing for the first time", deleted, service)
	}

	for backendName, r := range routings.http {
		u.applyHTTPRouteToDirectorAndBackendpool(backendName, httpRoute{}, r, deleted)
	}

	for backendName, r := range routings.grpc {
		u.applygRPCRouteToDirectorAndBackendpool(backendName, grpcRoute{}, r, deleted)
	}

	delete(u.lastSeenServices, service)
	return u.validatedAndReturnConfigs()
}

func (u *updater) onModifiedOrAddedEvent(serviceObj service, service serviceKey, eType eventType) (*pb_config.DirectorConfig, *pb_config.BackendPoolConfig, error) {
	routings, ok := u.lastSeenServices[service]
	if ok && eType == added {
		return nil, nil, errors.Errorf("Got %s event for item %v that already exists", eType, service)
	}

	if !ok {
		if eType == modified {
			return nil, nil, errors.Errorf("Got %s event for item %v that we are seeing for the first time", eType, service)
		}
		routings = serviceRoutings{
			http: make(map[string]httpRoute),
			grpc: make(map[string]grpcRoute),
		}
	}

	annotations := serviceObj.Metadata.Annotations
	if serviceObj.Metadata.Annotations == nil {
		annotations = map[string]string{}
	}

	foundRoutings := struct {
		http map[string]struct{}
		grpc map[string]struct{}
	}{
		http: make(map[string]struct{}),
		grpc: make(map[string]struct{}),
	}
	for annotationKey, value := range annotations {
		var portToExpose string
		if strings.HasPrefix(annotationKey, u.httpAnnotationPrefix) {
			portToExpose = strings.TrimPrefix(annotationKey, u.httpAnnotationPrefix)
		}

		if strings.HasPrefix(annotationKey, u.grpcAnnotationPrefix) {
			portToExpose = strings.TrimPrefix(annotationKey, u.grpcAnnotationPrefix)
		}

		if portToExpose == "" {
			// Not our annotation.
			continue
		}

		// NOTE: There is no check if this port actually is exposed by serviceObj!
		backendName := fmt.Sprintf("%s_%s_%s", service.name, service.namespace, portToExpose)
		domainPort := fmt.Sprintf("%s.%s:%s", service.name, service.namespace, portToExpose)

		mainMatcher, portMatcher, err := u.annotationValueToMatchers(service.name, annotationKey, value)
		if err != nil {
			return nil, nil, err
		}

		if strings.HasPrefix(annotationKey, u.httpAnnotationPrefix) {
			route := httpRoute{
				hostMatcher: mainMatcher,
				portMatcher: portMatcher,
				domainPort:  domainPort,
			}
			u.applyHTTPRouteToDirectorAndBackendpool(backendName, route, httpRoute{}, eType)
			foundRoutings.http[backendName] = struct{}{}
			routings.http[backendName] = route
			continue
		}

		if strings.HasPrefix(annotationKey, u.grpcAnnotationPrefix) {
			route := grpcRoute{
				serviceNameMatcher: mainMatcher,
				portMatcher:        portMatcher,
				domainPort:         domainPort,
			}
			u.applygRPCRouteToDirectorAndBackendpool(backendName, route, grpcRoute{}, eType)
			foundRoutings.grpc[backendName] = struct{}{}
			routings.grpc[backendName] = route
			continue
		}
	}

	// What to remove?
	for backend, r := range routings.http {
		if _, ok := foundRoutings.http[backend]; ok {
			continue
		}

		// Not found, so it needs to be removed.
		u.applyHTTPRouteToDirectorAndBackendpool(backend, httpRoute{}, r, eType)
		delete(routings.http, backend)
	}

	for backend, r := range routings.grpc {
		if _, ok := foundRoutings.grpc[backend]; ok {
			continue
		}

		// Not found, so it needs to be removed.
		u.applygRPCRouteToDirectorAndBackendpool(backend, grpcRoute{}, r, eType)
		delete(routings.grpc, backend)
	}

	u.lastSeenServices[service] = routings
	return u.validatedAndReturnConfigs()
}

func (u *updater) validatedAndReturnConfigs() (*pb_config.DirectorConfig, *pb_config.BackendPoolConfig, error) {
	err := u.currentDirectorConfig.Validate()
	if err != nil {
		return nil, nil, errors.Wrap(err, "director config does not pass validation after generation.")
	}

	err = u.currentBackendConfig.Validate()
	if err != nil {
		return nil, nil, errors.Wrap(err, "backendpool config does not pass validation after generation.")
	}

	u.sortConfigs()
	return u.currentDirectorConfig, u.currentBackendConfig, nil
}

func (u *updater) annotationValueToMatchers(serviceName string, key, value string) (string, uint32, error) {
	split := strings.Split(value, ":")
	mainMatcher := split[0]
	if mainMatcher == "" {
		mainMatcher = fmt.Sprintf("%s.%s", serviceName, u.externalDomainSuffix)
	}

	portMatcher := uint32(0)
	if len(split) > 1 && split[1] != "" {
		portMatcherInt, err := strconv.Atoi(split[1])
		if err != nil {
			return "", 0, errors.Errorf("Wrong format of %s annotation value %s. In hostport, expected port to be empty or valid uint32. Got %s", key, value, split[1])
		}
		portMatcher = uint32(portMatcherInt)
	}

	return mainMatcher, portMatcher, nil
}

func (u *updater) applyHTTPRouteToDirectorAndBackendpool(backendName string, newRoute httpRoute, oldRoute httpRoute, eventType eventType) {
	if eventType == deleted || eventType == modified {
		toDeleteIndex := -1
		for i, directorRoute := range u.currentDirectorConfig.GetHttp().Routes {
			if !directorRoute.Autogenerated {
				// Do not modify not generated ones.
				continue
			}

			if directorRoute.HostMatcher == oldRoute.hostMatcher &&
				directorRoute.PortMatcher == oldRoute.portMatcher {
				toDeleteIndex = i
				break
			}
		}

		if toDeleteIndex >= 0 {
			u.currentDirectorConfig.GetHttp().Routes = append(
				u.currentDirectorConfig.GetHttp().Routes[:toDeleteIndex],
				u.currentDirectorConfig.GetHttp().Routes[toDeleteIndex+1:]...,
			)
		}

		toDeleteIndex = -1
		for i, backends := range u.currentBackendConfig.GetHttp().Backends {
			if !backends.Autogenerated {
				// Do not modify not generated ones.
				continue
			}

			if backends.Name == backendName {
				toDeleteIndex = i
				break
			}
		}

		if toDeleteIndex >= 0 {
			u.currentBackendConfig.GetHttp().Backends = append(
				u.currentBackendConfig.GetHttp().Backends[:toDeleteIndex],
				u.currentBackendConfig.GetHttp().Backends[toDeleteIndex+1:]...,
			)
		}
	}

	var empty httpRoute
	if newRoute == empty {
		return
	}

	if eventType == added || eventType == modified {
		u.currentDirectorConfig.GetHttp().Routes = append(
			u.currentDirectorConfig.GetHttp().Routes,
			&pb_httproutes.Route{
				Autogenerated: true,
				BackendName:   backendName,
				HostMatcher:   newRoute.hostMatcher,
				PortMatcher:   newRoute.portMatcher,
				ProxyMode:     pb_httproutes.ProxyMode_REVERSE_PROXY,
			},
		)

		u.currentBackendConfig.GetHttp().Backends = append(
			u.currentBackendConfig.GetHttp().Backends,
			&pb_httpbackends.Backend{
				Autogenerated: true,
				Name:          backendName,
				Resolver: &pb_httpbackends.Backend_K8S{
					K8S: &pb_resolvers.K8SResolver{
						DnsPortName: newRoute.domainPort,
					},
				},
				Balancer: pb_httpbackends.Balancer_ROUND_ROBIN,
			},
		)
	}
}

func (u *updater) applygRPCRouteToDirectorAndBackendpool(backendName string, newRoute grpcRoute, oldRoute grpcRoute, eventType eventType) {
	if eventType == deleted || eventType == modified {
		toDeleteIndex := -1
		for i, directorRoute := range u.currentDirectorConfig.GetGrpc().Routes {
			if !directorRoute.Autogenerated {
				// Do not modify not generated ones.
				continue
			}

			if directorRoute.ServiceNameMatcher == oldRoute.serviceNameMatcher &&
				directorRoute.PortMatcher == oldRoute.portMatcher {
				toDeleteIndex = i
				break
			}
		}

		if toDeleteIndex >= 0 {
			u.currentDirectorConfig.GetGrpc().Routes = append(
				u.currentDirectorConfig.GetGrpc().Routes[:toDeleteIndex],
				u.currentDirectorConfig.GetGrpc().Routes[toDeleteIndex+1:]...,
			)
		}

		toDeleteIndex = -1
		for i, backends := range u.currentBackendConfig.GetGrpc().Backends {
			if !backends.Autogenerated {
				// Do not modify not generated ones.
				continue
			}

			if backends.Name == backendName {
				toDeleteIndex = i
				break
			}
		}

		if toDeleteIndex >= 0 {
			u.currentBackendConfig.GetGrpc().Backends = append(
				u.currentBackendConfig.GetGrpc().Backends[:toDeleteIndex],
				u.currentBackendConfig.GetGrpc().Backends[toDeleteIndex+1:]...,
			)
		}
	}

	var empty grpcRoute
	if newRoute == empty {
		return
	}

	if eventType == added || eventType == modified {
		u.currentDirectorConfig.GetGrpc().Routes = append(
			u.currentDirectorConfig.GetGrpc().Routes,
			&pb_grpcroutes.Route{
				Autogenerated:      true,
				BackendName:        backendName,
				ServiceNameMatcher: newRoute.serviceNameMatcher,
				PortMatcher:        newRoute.portMatcher,
			},
		)

		u.currentBackendConfig.GetGrpc().Backends = append(
			u.currentBackendConfig.GetGrpc().Backends,
			&pb_grpcbackends.Backend{
				Autogenerated: true,
				Name:          backendName,
				Resolver: &pb_grpcbackends.Backend_K8S{
					K8S: &pb_resolvers.K8SResolver{
						DnsPortName: newRoute.domainPort,
					},
				},
				Balancer: pb_grpcbackends.Balancer_ROUND_ROBIN,
			},
		)
	}
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

func (u *updater) sortConfigs() {
	httpDirectorRouteSort(u.currentDirectorConfig.GetHttp().Routes)
	grpcDirectorRouteSort(u.currentDirectorConfig.GetGrpc().Routes)
	httpBackendpoolSort(u.currentBackendConfig.GetHttp().Backends)
	grpcBackendpoolSort(u.currentBackendConfig.GetGrpc().Backends)
}
