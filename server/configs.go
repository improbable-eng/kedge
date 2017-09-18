package main

import (
	"sync"

	"github.com/golang/protobuf/proto"
	"github.com/mwitkow/go-flagz/protobuf"
	"github.com/mwitkow/go-proto-validators"
	pb_config "github.com/mwitkow/kedge/_protogen/kedge/config"
	grpc_bp "github.com/mwitkow/kedge/grpc/backendpool"
	grpc_router "github.com/mwitkow/kedge/grpc/director/router"
	http_bp "github.com/mwitkow/kedge/http/backendpool"
	http_adhoc "github.com/mwitkow/kedge/http/director/adhoc"
	http_router "github.com/mwitkow/kedge/http/director/router"
	"github.com/mwitkow/kedge/lib/sharedflags"
	"github.com/sirupsen/logrus"
)

var (
	flagConfigDirector = protoflagz.DynProto3(sharedflags.Set,
		"kedge_config_director_config",
		&pb_config.DirectorConfig{
			Grpc: &pb_config.DirectorConfig_Grpc{},
			Http: &pb_config.DirectorConfig_Http{},
		},
		"Contents of the Kedge Director configuration. Dynamically settable or read from file").WithFileFlag("../misc/director.json").WithValidator(generalValidator).WithNotifier(directorConfigReload)

	flagConfigBackendpool = protoflagz.DynProto3(sharedflags.Set,
		"kedge_config_backendpool_config",
		&pb_config.BackendPoolConfig{
			Grpc: &pb_config.BackendPoolConfig_Grpc{},
			Http: &pb_config.BackendPoolConfig_Http{},
		},
		"Contents of the Kedge Backendpool configuration. Dynamically settable or read from file").WithFileFlag("../misc/backendpool.json").WithValidator(generalValidator).WithNotifier(backendConfigReloaded)

	grpcBackendPool = grpc_bp.NewDynamic(logrus.StandardLogger())
	httpBackendPool = http_bp.NewDynamic(logrus.StandardLogger())
	grpcRouter      = grpc_router.NewDynamic()
	httpRouter      = http_router.NewDynamic()
	httpAddresser   = http_adhoc.NewDynamic()

	backendMu sync.Mutex
)

func generalValidator(msg proto.Message) error {
	if val, ok := msg.(validator.Validator); ok {
		if err := val.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func directorConfigReload(_ proto.Message, newValue proto.Message) {
	newConfig := newValue.(*pb_config.DirectorConfig)

	// The gRPC and HTTP fields are guaranteed to be there because of validation.
	grpcRouter.Update(newConfig.GetGrpc().Routes)
	httpRouter.Update(newConfig.GetHttp().Routes)
	httpAddresser.Update(newConfig.GetHttp().AdhocRules)
}

func backendConfigReloaded(_ proto.Message, newValue proto.Message) {
	backendMu.Lock()
	defer backendMu.Unlock()

	newConfig := newValue.(*pb_config.BackendPoolConfig)

	// The gRPC and HTTP fields are guaranteed to be there because of validation.
	grpcBackendInNewConfig := make(map[string]struct{})
	grpcBackendInOldConfig := grpcBackendPool.Configs()
	grpcConfig := newConfig.GetGrpc()
	if grpcConfig != nil {
		for _, backend := range grpcConfig.Backends {
			_, err := grpcBackendPool.AddOrUpdate(backend, *flagLogTestBackendpoolResolution)
			if err != nil {
				logrus.Errorf("failed Adding or Updating grpc backend %v: %v", backend.Name, err)
			}
			grpcBackendInNewConfig[backend.Name] = struct{}{}
		}
	}

	for backendName := range grpcBackendInOldConfig {
		if _, exists := grpcBackendInNewConfig[backendName]; !exists {
			logrus.Infof("removing gRPC backend: %v", backendName)
			grpcBackendPool.Remove(backendName)
		}
	}

	httpBackendInNewConfig := make(map[string]struct{})
	httpBackendInOldConfig := httpBackendPool.Configs()
	httpConfig := newConfig.GetHttp()
	if httpConfig != nil {
		for _, backend := range newConfig.GetHttp().Backends {
			_, err := httpBackendPool.AddOrUpdate(backend, *flagLogTestBackendpoolResolution)
			if err != nil {
				logrus.Errorf("failed creating http backend %v: %v", backend.Name, err)
			}
			httpBackendInNewConfig[backend.Name] = struct{}{}
		}
	}

	for backendName := range httpBackendInOldConfig {
		if _, exists := httpBackendInNewConfig[backendName]; !exists {
			logrus.Infof("removing http backend: %v", backendName)
			httpBackendPool.Remove(backendName)
		}
	}
}
