package discovery

import (
	"fmt"
	"strings"

	pb_config "github.com/mwitkow/kedge/_protogen/kedge/config"
	"github.com/pkg/errors"
)

type route struct {
	// Host (HTTP) or Service (gRPC).
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

	tlsConfigs map[backendName]struct{}
}

type serviceConf struct {
	routings serviceRoutings
	backends serviceBackends
}

type updater struct {
	baseDirectorConfig    *pb_config.DirectorConfig
	baseBackendConfig     *pb_config.BackendPoolConfig
	externalDomainSuffix  string
	labelAnnotationPrefix string

	lastSeenServices map[serviceKey]serviceConf
}

func newUpdater(
	baseDirector *pb_config.DirectorConfig,
	baseBackendpool *pb_config.BackendPoolConfig,
	externalDomainSuffix string,
	labelAnnotationPrefix string,
) *updater {
	return &updater{
		baseDirectorConfig:    baseDirector,
		baseBackendConfig:     baseBackendpool,
		externalDomainSuffix:  externalDomainSuffix,
		labelAnnotationPrefix: labelAnnotationPrefix,
		lastSeenServices:      make(map[serviceKey]serviceConf),
	}
}

func (u *updater) onEvent(e event) (*pb_config.DirectorConfig, *pb_config.BackendPoolConfig, error) {
	serviceObj := e.Object
	service := serviceKey{serviceObj.Metadata.Name, serviceObj.Metadata.Namespace}

	var err error
	switch e.Type {
	case deleted:
		err = u.onDeletedEvent(service)
	case added, modified:
		err = u.onModifiedOrAddedEvent(serviceObj, service, e.Type)
	default:
		err = errors.Errorf("Got not supported event type %s", e.Type)
	}
	if err != nil {
		return nil, nil, err
	}

	return u.lastSeenServicesToConfigs()
}

// onDeletedEvent creates removes deleted serviceConfig from lastSeenServices map.
func (u *updater) onDeletedEvent(service serviceKey) error {
	_, ok := u.lastSeenServices[service]
	if !ok {
		return errors.Errorf("Got %s event for item %v that we are seeing for the first time", deleted, service)
	}

	delete(u.lastSeenServices, service)
	return nil
}

func (u *updater) hostMatcherAnnotation() string {
	return fmt.Sprintf("%s%s", u.labelAnnotationPrefix, hostMatcherAnnotationSuffix)
}

func (u *updater) serviceNameMatcherAnnotation() string {
	return fmt.Sprintf("%s%s", u.labelAnnotationPrefix, serviceNameMatcherAnnotationSuffix)
}

// onModifiedOrAddedEvent creates new serviceConfig and places it in lastSeenServices map.
func (u *updater) onModifiedOrAddedEvent(serviceObj service, service serviceKey, eType eventType) error {
	_, ok := u.lastSeenServices[service]
	if ok && eType == added {
		return errors.Errorf("Got %s event for item %v that already exists", eType, service)
	}

	if !ok {
		if eType == modified {
			return errors.Errorf("Got %s event for item %v that we are seeing for the first time", eType, service)
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
		tlsConfigs:      make(map[backendName]struct{}),
	}

	for _, port := range serviceObj.Spec.Ports {
		// NOTE: There is no check if this port actually is exposed by serviceObj.
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

		scheme := getScheme(port.Name)
		switch scheme {
		case httpScheme:
			fallthrough
		case httptlsScheme:
			if hostMatcherOverride != "" {
				foundRoute.nameMatcher = hostMatcherOverride
			}

			// We need to avoid specific ports if possible, since Golang removes the port from request.URL.Port() for default ports,
			// even when user specifies httpScheme://<host>:80 or https://<host>:443 explicitly. As a result we convert default
			// ports to NO port, which mean host-wide routing.
			if scheme == httpScheme && port.Port == 80 {
				foundRoute.portMatcher = 0
			}
			if scheme == httptlsScheme && port.Port == 443 {
				foundRoute.portMatcher = 0
			}

			foundRoutes.http[backendName] = append(foundRoutes.http[backendName], foundRoute)
			// Since target port is the same we can use whatever domainPort we have here.
			foundBackends.httpDomainPorts[backendName] = domainPort

			if scheme == httptlsScheme {
				// TODO(bplotka): Add support for customizing the TLS config (or setting it to actually verify!) using service annotations.
				foundBackends.tlsConfigs[backendName] = struct{}{}
			}
		case grpcScheme:
			fallthrough
		case grpctlsScheme:
			if serviceNameMatcherOverride != "" {
				foundRoute.nameMatcher = serviceNameMatcherOverride
			}

			foundRoutes.grpc[backendName] = append(foundRoutes.grpc[backendName], foundRoute)
			// Since target port is the same we can use whatever domainPort we have here.
			foundBackends.grpcDomainPorts[backendName] = domainPort

			if scheme == grpctlsScheme {
				// TODO(bplotka): Add support for customizing the TLS config (or setting it to actually verify!) using service annotations.
				foundBackends.tlsConfigs[backendName] = struct{}{}
			}
		}

	}

	u.lastSeenServices[service] = serviceConf{
		routings: foundRoutes,
		backends: foundBackends,
	}
	return nil
}

type protocolType int

const (
	httpScheme    protocolType = iota
	httptlsScheme
	grpcScheme
	grpctlsScheme
	unknownScheme
)

func getScheme(portName string) protocolType {
	if portName == "http" || strings.HasPrefix(portName, "http-") {
		return httpScheme
	}

	if portName == "httptls" || strings.HasPrefix(portName, "httptls-") {
		return httptlsScheme
	}

	if portName == "grpc" || strings.HasPrefix(portName, "grpc-") {
		return grpcScheme
	}

	if portName == "grpctls" || strings.HasPrefix(portName, "grpctls-") {
		return grpctlsScheme
	}

	return unknownScheme
}
