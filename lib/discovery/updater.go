package discovery

import (
	"fmt"
	"sort"
	"strings"

	pb_config "github.com/mwitkow/kedge/_protogen/kedge/config"
	pb_grpcbackends "github.com/mwitkow/kedge/_protogen/kedge/config/grpc/backends"
	pb_grpcroutes "github.com/mwitkow/kedge/_protogen/kedge/config/grpc/routes"
	pb_httpbackends "github.com/mwitkow/kedge/_protogen/kedge/config/http/backends"
	pb_httproutes "github.com/mwitkow/kedge/_protogen/kedge/config/http/routes"
	"github.com/pkg/errors"
)

type route struct {
	// Host (HTTP) or Service (Grpc).
	nameMatcher string
	// If 0 it means just name matcher only.
	portMatcher uint32
}

type serviceKey struct {
	name, namespace string
}

type backendName struct {
	service    serviceKey
	targetPort string
}

func (b backendName) String() string {
	backendName := fmt.Sprintf("%s_%s_%v", b.service.name, b.service.namespace, b.targetPort)
	// BackendName needs to conform regex: "^[a-z_0-9.]{2,64}$"
	return strings.Replace(backendName, "-", "_", -1)
}

type serviceRoutings struct {
	// There could be more than one director routing per backend.
	http map[backendName][]route
	grpc map[backendName][]route
}

type serviceBackends struct {
	httpDomainPorts map[backendName]string
	grpcDomainPorts map[backendName]string
}

type updater struct {
	currentDirectorConfig *pb_config.DirectorConfig
	currentBackendConfig  *pb_config.BackendPoolConfig
	externalDomainSuffix  string
	labelAnnotationPrefix string

	lastSeenServices map[serviceKey]serviceRoutings
}

func newUpdater(
	baseDirector *pb_config.DirectorConfig,
	baseBackendpool *pb_config.BackendPoolConfig,
	externalDomainSuffix string,
	labelAnnotationPrefix string,
) *updater {
	resultDirectorConfig, resultBackendPool := cloneConfigs(baseDirector, baseBackendpool)
	return &updater{
		currentDirectorConfig: resultDirectorConfig,
		currentBackendConfig:  resultBackendPool,
		externalDomainSuffix:  externalDomainSuffix,
		labelAnnotationPrefix: labelAnnotationPrefix,
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

	for backend, routes := range routings.http {
		u.applyHTTPRouteToDirector(backend.String(), routes, []route{})
		u.applyHTTPRouteToBackendpool(backend, map[backendName]string{})
	}

	for backend, routes := range routings.grpc {
		u.applygRPCRouteToDirector(backend.String(), routes, []route{})
		u.applygRPCRouteToBackendpool(backend, map[backendName]string{})
	}

	delete(u.lastSeenServices, service)
	return u.validatedAndReturnConfigs()
}

func (u *updater) hostMatcherAnnotation() string {
	return fmt.Sprintf("%s%s", u.labelAnnotationPrefix, hostMatcherAnnotationSuffix)
}

func (u *updater) serviceNameMatcherAnnotation() string {
	return fmt.Sprintf("%s%s", u.labelAnnotationPrefix, serviceNameMatcherAnnotationSuffix)
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
			http: make(map[backendName][]route),
			grpc: make(map[backendName][]route),
		}
	}

	var hostMatcherOverride string
	if serviceObj.Metadata.Annotations != nil {
		hostMatcherOverride = serviceObj.Metadata.Annotations[u.hostMatcherAnnotation()]
	}
	var serviceNameMatcherOverride string
	if serviceObj.Metadata.Annotations != nil {
		serviceNameMatcherOverride = serviceObj.Metadata.Annotations[u.serviceNameMatcherAnnotation()]
	}

	foundRoutes := serviceRoutings{
		http: make(map[backendName][]route),
		grpc: make(map[backendName][]route),
	}
	foundBackends := serviceBackends{
		httpDomainPorts: make(map[backendName]string),
		grpcDomainPorts: make(map[backendName]string),
	}
	for _, port := range serviceObj.Spec.Ports {
		// NOTE: There is no check if this port actually is exposed by serviceObj!
		portToExpose := port.TargetPort
		if portToExpose == nil {
			portToExpose = port.Port
		}

		backendName := backendName{
			service:    service,
			targetPort: fmt.Sprintf("%v", portToExpose),
		}
		domainPort := fmt.Sprintf("%s.%s:%v", service.name, service.namespace, port.Name)
		foundRoute := route{
			nameMatcher: fmt.Sprintf("%s.%s", service.name, u.externalDomainSuffix),
			portMatcher: port.Port,
		}
		if port.Name == "http" || strings.HasPrefix(port.Name, "http-") {
			if hostMatcherOverride != "" {
				foundRoute.nameMatcher = hostMatcherOverride
			}

			if port.Port == 80 {
				// Avoid specific ports if possible.
				foundRoute.portMatcher = 0
			}

			foundRoutes.http[backendName] = append(foundRoutes.http[backendName], foundRoute)
			// Since target port is the same we can use whatever domainPort we have here.
			foundBackends.httpDomainPorts[backendName] = domainPort
			continue
		}

		if port.Name == "grpc" || strings.HasPrefix(port.Name, "grpc-") {
			if serviceNameMatcherOverride != "" {
				foundRoute.nameMatcher = serviceNameMatcherOverride
			}

			foundRoutes.grpc[backendName] = append(foundRoutes.grpc[backendName], foundRoute)
			// Since target port is the same we can use whatever domainPort we have here.
			foundBackends.grpcDomainPorts[backendName] = domainPort
			continue
		}
	}
	u.applyHTTPRoutes(routings, foundRoutes, foundBackends)
	u.applygRPCRoutes(routings, foundRoutes, foundBackends)
	u.lastSeenServices[service] = foundRoutes

	return u.validatedAndReturnConfigs()
}

func (u *updater) applyHTTPRoutes(oldRoutings serviceRoutings, newRoutings serviceRoutings, backendTargets serviceBackends) {
	// What to add/change per backend?
	for backend := range backendTargets.httpDomainPorts {
		u.applyHTTPRouteToDirector(backend.String(), oldRoutings.http[backend], newRoutings.http[backend])
		u.applyHTTPRouteToBackendpool(backend, backendTargets.httpDomainPorts)
	}

	// What backend to remove completely?
	for backend, r := range oldRoutings.http {
		if _, ok := newRoutings.http[backend]; ok {
			continue
		}
		// Not found, so it needs to be removed.
		u.applyHTTPRouteToDirector(backend.String(), r, []route{})
		u.applyHTTPRouteToBackendpool(backend, backendTargets.httpDomainPorts)
	}
}

func (u *updater) applygRPCRoutes(oldRoutings serviceRoutings, newRoutings serviceRoutings, backendTargets serviceBackends) {
	// What to add/change per backend?
	for backend := range backendTargets.grpcDomainPorts {
		u.applygRPCRouteToDirector(backend.String(), oldRoutings.grpc[backend], newRoutings.grpc[backend])
		u.applygRPCRouteToBackendpool(backend, backendTargets.grpcDomainPorts)
	}

	// What backend to remove completely?
	for backend, r := range oldRoutings.grpc {
		if _, ok := newRoutings.grpc[backend]; ok {
			continue
		}
		// Not found, so it needs to be removed.
		u.applygRPCRouteToDirector(backend.String(), r, []route{})
		u.applygRPCRouteToBackendpool(backend, backendTargets.grpcDomainPorts)
	}
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
