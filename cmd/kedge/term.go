package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/improbable-eng/kedge/pkg/sharedflags"
	"github.com/sirupsen/logrus"
)

var (
	signalShutdownTimeout = sharedflags.Set.Duration(
		"server_sigterm_timeout",
		40*time.Second,
		"Timeout for graceful Shutdown after catching a signal. "+
			"If the timeout expires, another SIGTERM is sent that skips the Shutdown handler")
)

// waitForAny blocks until any of given signal is received, or context is done.
// The SIGTERM is signaled after some time, delaying it for servers to have time to graceful shutdown.
func waitForAny(ctx context.Context, signals ...os.Signal) {
	ch := make(chan os.Signal, len(signals))
	signal.Notify(ch, signals...)
	defer signal.Stop(ch)

	var gotSignal os.Signal
	select {
	case <-ctx.Done():
		return
	case gotSignal = <-ch:
		logrus.Infof("Got %s signal", gotSignal.String())
	}

	// Make sure we finally do SIGTERM after given time.
	go func() {
		ch := make(chan os.Signal, len(signals))
		signal.Notify(ch, signals...)
		defer signal.Stop(ch)

		select {
		case <-ctx.Done():
			return
		case <-ch:
			return
		case <-time.After(*signalShutdownTimeout):
		}

		logrus.Infof("Graceful Shutdown after %s timed out, re-sending SIGTERM.", gotSignal.String())
		p, err := os.FindProcess(os.Getpid())
		if err != nil {
			logrus.Errorf("Can't find process %v", err)
		}
		p.Signal(syscall.SIGTERM)
	}()
}
