package main

import (
	pb_config "github.com/mwitkow/kedge/_protogen/kedge/config"
	"github.com/mwitkow/kedge/http/backendpool"
	"github.com/sirupsen/logrus"
)

func testLogBackendpool(logger logrus.FieldLogger) {
	logger.Warn("Flag check_backendpool_and_exit specified. Performing test resolution.")
	cfg := flagConfigBackendpool.Get().(*pb_config.BackendPoolConfig)
	b, err := backendpool.NewStatic(cfg.GetHttp().GetBackends())
	if err != nil {
		logger.WithError(err).Error("Error while creating static backendpool")
		return
	}

	b.LogTestResolution(logger)
}
