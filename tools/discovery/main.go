package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	pb_config "github.com/improbable-eng/kedge/protogen/kedge/config"
	"github.com/improbable-eng/kedge/lib/discovery"
	"github.com/improbable-eng/kedge/lib/sharedflags"
	"github.com/sirupsen/logrus"
	"time"
)

var (
	flagLogLevel = sharedflags.Set.String("log_level", "info", "Log level")
)

func main() {
	if err := sharedflags.Set.Parse(os.Args); err != nil {
		logrus.WithError(err).Fatal("failed parsing flags")
	}

	lvl, err := logrus.ParseLevel(*flagLogLevel)
	if err != nil {
		logrus.WithError(err).Fatal("Cannot parse log level: %s", *flagLogLevel)
	}
	logrus.SetLevel(lvl)

	logger := logrus.StandardLogger()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = generateRoutings(ctx, logger)
	if err != nil {
		logger.WithError(err).Fatal("Failed to generate Routings")
	}
}

func generateRoutings(ctx context.Context, logger logrus.FieldLogger) error {
	backendDiscovery, err := discovery.NewFromFlags(logger, &pb_config.DirectorConfig{}, &pb_config.BackendPoolConfig{})
	if err != nil {
		return err
	}

	director, backendpool, err := backendDiscovery.DiscoverOnce(ctx, 3 * time.Second)
	if err != nil {
		return err
	}

	directorCfg, err := json.MarshalIndent(director, "", "  ")
	if err != nil {
		return err
	}

	backendpoolCfg, err := json.MarshalIndent(backendpool, "", "  ")
	if err != nil {
		return err
	}

	fmt.Println(string(directorCfg))
	fmt.Println(string(backendpoolCfg))
	return nil

}
