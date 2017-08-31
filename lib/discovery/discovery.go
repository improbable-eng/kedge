package discovery

import (
	"context"
	"time"

	pb_config "github.com/mwitkow/kedge/_protogen/kedge/config"
	"github.com/mwitkow/kedge/lib/k8s"
	"github.com/mwitkow/kedge/lib/sharedflags"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

var (
	flagExternalDomainSuffix = sharedflags.Set.String("discovery_external_domain_suffix", "", "Required suffix "+
		"that will be added to service name to constructs external domain for director route")
	flagLabelSelector = sharedflags.Set.String("discovery_service_selector", "kedge-exposed",
		"Expected label on kubernetes service to be present if service wants to be exposed by kedge."+
			"Kedge will use this label as selector for value 'true'")
	flagHTTPAnnotationPrefix = sharedflags.Set.String("discovery_service_http_annotation_prefix", "http.exposed.kedge.com/",
		"Expected annotation prefix for kubernetes service to be present if service wants to expose HTTP port by kedge."+
			"Kedge will use this annotation as director route -> backend pair. Value of this URL must be host_matcher:port_matcher.")
	flagGRPCAnnotationPrefix = sharedflags.Set.String("discovery_service_grpc_annotation_prefix", "grpc.exposed.kedge.com/",
		"Expected annotation prefix for kubernetes service to be present if service wants to expose GRPC port by kedge."+
			"Kedge will use this annotation as director route -> backend pair. Value of this must be service_name_matcher:port_matcher.")
)

// Routing Discovery allows to get fresh director and backendpool configuration filled with autogenerated routings based on service annotations.
// It watches every services (from whatever namespace) that have label name defined in 'discovery_service_selector'.
// It checks every service for annotation with prefix defined in 'discovery_service_http_annotation_prefix' (for HTTP)
// and 'discovery_service_grpc_annotation_prefix' (for gRPC) flags and generates routing->backend pair
// based on port given in the annotation.
//
// The expected format of the annotation is:
//
// For HTTP:
// 	"annotation-http-prefix/<service-port-to-expose>=
// Output:
// 		host_matcher: "service.<discovery_external_domain_suffix>"
//      port_matcher: (no port matching)
//
// 	"annotation-http-prefix/<service-port-to-expose>=:<uint32 port as routing port_matcher>
// Output:
// 		host_matcher: "service.<discovery_external_domain_suffix>"
//      port_matcher: as given
//
// 	"annotation-http-prefix/<service-port-to-expose>=<external domain as host_matcher>
// Output:
// 		host_matcher: as given
//      port_matcher: (no port matching)
//
//  "annotation-http-prefix/<service-port-to-expose>=<external domain as host_matcher>:<uint32 port as routing port_matcher>
// Output:
// 		host_matcher: as given
//      port_matcher: as given
//
// Similar for GRPC:
// 	"annotation-grpc-prefix/<service-port-to-expose>=
// 	"annotation-grpc-prefix/<service-port-to-expose>=:<uint32 port as routing port_matcher>
// 	"annotation-grpc-prefix/<service-port-to-expose>=<external service name as service_name_matcher>
//  "annotation-grpc-prefix/<service-port-to-expose>=<external service name as service_name_matcher>:<uint32 port as routing port_matcher>
//
// NOTE:
// - backend name is always in form of <service>_<namespace>_<service-port-to-expose>
// - <service-port-to-expose> can be in both port name or port number form.
// - no check for duplicated host_matchers in annotations or between autogenerated & base ones (!)
// - no check if the target port inside service actually exists.
type RoutingDiscovery struct {
	logger               logrus.FieldLogger
	serviceClient        ServiceClient
	baseBackendpool      *pb_config.BackendPoolConfig
	baseDirector         *pb_config.DirectorConfig
	labelSelectorKey     string
	externalDomainSuffix string
	httpAnnotationPrefix string
	grpcAnnotationPrefix string
}

func NewFromFlags(logger logrus.FieldLogger, baseDirector *pb_config.DirectorConfig, baseBackendpool *pb_config.BackendPoolConfig) (*RoutingDiscovery, error) {
	if *flagExternalDomainSuffix == "" {
		return nil, errors.Errorf("required flag 'discovery_external_domain_suffix' is not specified.")
	}

	apiClient, err := k8s.NewFromFlags()
	if err != nil {
		return nil, err
	}
	return NewWithClient(logger, baseDirector, baseBackendpool, &client{k8sClient: apiClient}), nil
}

// NewWithClient returns a new Kubernetes RoutingDiscovery using given k8s.APIClient configured to be used against kube-apiserver.
func NewWithClient(logger logrus.FieldLogger, baseDirector *pb_config.DirectorConfig, baseBackendpool *pb_config.BackendPoolConfig, serviceClient ServiceClient) *RoutingDiscovery {
	return &RoutingDiscovery{
		logger:               logger,
		baseBackendpool:      baseBackendpool,
		baseDirector:         baseDirector,
		serviceClient:        serviceClient,
		labelSelectorKey:     *flagLabelSelector,
		externalDomainSuffix: *flagExternalDomainSuffix,
		httpAnnotationPrefix: *flagHTTPAnnotationPrefix,
		grpcAnnotationPrefix: *flagGRPCAnnotationPrefix,
	}
}

// DiscoverOnce returns director & backendpool configs filled with mix of persistent routes & backends given in base configs and dynamically discovered ones.
func (d *RoutingDiscovery) DiscoverOnce(ctx context.Context) (*pb_config.DirectorConfig, *pb_config.BackendPoolConfig, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second) // Let's give 4 seconds to gather all changes.
	defer cancel()

	watchResultCh := make(chan watchResult)
	defer close(watchResultCh)

	err := startWatchingServicesChanges(ctx, d.labelSelectorKey, d.serviceClient, watchResultCh)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "Failed to start watching services by %s selector stream", d.labelSelectorKey)
	}

	updater := newUpdater(
		d.baseDirector,
		d.baseBackendpool,
		d.externalDomainSuffix,
		d.httpAnnotationPrefix,
		d.grpcAnnotationPrefix,
	)

	var resultDirectorConfig *pb_config.DirectorConfig
	var resultBackendPool *pb_config.BackendPoolConfig
	for {
		var event event
		select {
		case <-ctx.Done():
			// Time is up, let's return what we have so far.
			return resultDirectorConfig, resultBackendPool, nil
		case r := <-watchResultCh:
			if r.err != nil {
				return nil, nil, errors.Wrap(r.err, "error on reading event stream")
			}
			event = *r.ep
		}

		resultDirectorConfig, resultBackendPool, err = updater.onEvent(event)
		if err != nil {
			return nil, nil, errors.Wrapf(err, "error on updating routing on event %v", event)
		}
	}
}