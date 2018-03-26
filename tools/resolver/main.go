package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/improbable-eng/kedge/pkg/resolvers/k8s"
	"github.com/improbable-eng/kedge/pkg/sharedflags"
	"github.com/oklog/run"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/naming"
)

var (
	flagLogLevel = sharedflags.Set.String("log_level", "info", "Log level")
	flagTarget   = sharedflags.Set.String("target", "", "Target entry to resolver and watch for new resolutions")
)

func main() {
	if err := sharedflags.Set.Parse(os.Args); err != nil {
		logrus.WithError(err).Fatal("failed parsing flags")
	}

	lvl, err := logrus.ParseLevel(*flagLogLevel)
	if err != nil {
		logrus.WithError(err).Fatalf("Cannot parse log level: %s", *flagLogLevel)
	}
	logrus.SetLevel(lvl)
	logrus.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})

	resolver, err := k8sresolver.NewFromFlags(logrus.StandardLogger())
	if err != nil {
		logrus.WithError(err).Fatal("Cannot create k8s resolver")
	}

	watcher, err := resolver.Resolve(*flagTarget)
	if err != nil {
		logrus.WithError(err).Fatalf("Failed to resolve %s", *flagTarget)
	}
	defer watcher.Close()

	var (
		g     run.Group
		state = map[string]struct{}{}
	)

	{
		ctx, cancel := context.WithCancel(context.Background())
		g.Add(func() error {
			for ctx.Err() == nil {
				updates, err := watcher.Next()
				if err != nil {
					return err
				}

				var msg string
				for _, up := range updates {
					if up.Op == naming.Add {
						state[up.Addr] = struct{}{}
					} else {
						delete(state, up.Addr)
					}
					msg += fmt.Sprintf("[op: %v, addr: %s]", up.Op, up.Addr)
				}
				fmt.Printf("Got Updates: %s\nOverall state: %v\n", msg, state)
			}

			return nil
		}, func(error) {
			watcher.Close()
			cancel()
		})
	}
	{
		cancel := make(chan struct{})
		g.Add(func() error {
			return interrupt(cancel)
		}, func(error) {
			logrus.Infof("\nReceived an interrupt, stopping services...\n")
			close(cancel)
		})
	}

	logrus.Info("Starting standalone resolver")
	if err := g.Run(); err != nil {
		logrus.WithError(err).Fatal("Command finished.")
	}
}

func interrupt(cancel <-chan struct{}) error {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-c:
		return nil
	case <-cancel:
		return errors.New("canceled")
	}
}
