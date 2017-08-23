package main

import (
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/mwitkow/kedge/lib/resolvers/k8s"
	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
	"google.golang.org/grpc/naming"
)

func main() {
	pflag.CommandLine.AddFlagSet(k8sresolver.FlagSet)
	pflag.Parse()

	logger := logrus.New()

	if len(os.Args) < 2 || strings.HasPrefix(os.Args[1], "--") {
		logger.Fatalf("Expected first argument to be in form of %s", k8sresolver.ExpectedTargetFmt)
	}

	target := os.Args[1]

	resolver, err := k8sresolver.NewFromFlags(logger)
	if err != nil {
		logger.WithError(err).Fatal("Failed to create new resolver from flags")
	}

	watcher, err := resolver.Resolve(target)
	if err != nil {
		logger.WithError(err).Fatal("Failed to get watcher from resolver")
	}

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM)
	defer signal.Stop(ch)

	go func() {
		for {
			updates, err := watcher.Next()
			if err != nil {
				logger.WithError(err).Errorf("Got error on watcher.Next")
				continue
			}

			for _, up := range updates {
				logger.Infof("Got update %s for addr: %s", opToString(up.Op), up.Addr)
			}
		}
	}()
	select {
	case <-ch:
		watcher.Close()
		return
	}
}

func opToString(op naming.Operation) string {
	switch op {
	case naming.Add:
		return "Add"
	case naming.Delete:
		return "Delete"
	}
	return "unknown"
}
