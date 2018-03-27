package discovery

import (
	"context"
	"encoding/json"
	"io"

	"io/ioutil"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type watchResult struct {
	ep  *event
	err error
}

// startWatchingServicesChanges starts a stream that in go routine reads from connection for every change event.
// All errors are assumed irrecoverable, it is a caller responsibility to recreate stream on EOF, error event etc.
// We read connection from separate go routine because read is blocking with no timeout/cancel logic.
func startWatchingServicesChanges(
	ctx context.Context,
	labelSelector string,
	serviceClient serviceClient,
	eventsCh chan<- watchResult,
) error {
	innerCtx, innerCancel := context.WithCancel(ctx)
	stream, err := serviceClient.StartChangeStream(innerCtx, labelSelector)
	if err != nil {
		innerCancel()
		return errors.Wrapf(err, "discovery stream: Failed to do start stream for label %s", labelSelector)
	}

	go func() {
		select {
		case <-innerCtx.Done():
			// Request is cancelled, so we need to read what is left there to not leak go routines.
			_, _ = ioutil.ReadAll(stream)
			err = stream.Close()
			if err != nil {
				logrus.WithError(err).Warn("discovery: Failed to Close cancelled stream connection")
			}
		}
	}()

	go func() {
		proxyAllEvents(innerCtx, json.NewDecoder(stream), eventsCh)
		innerCancel()
	}()

	return nil
}

type eventType string

const (
	added    eventType = "ADDED"
	modified eventType = "MODIFIED"
	deleted  eventType = "DELETED"
	failed   eventType = "ERROR"
)

// event represents a single event to a watched resource.
type event struct {
	Type   eventType `json:"type"`
	Object service   `json:"object"`
}

type service struct {
	Kind       string   `json:"kind"`
	APIVersion string   `json:"apiVersion"`
	Metadata   metadata `json:"metadata"`
	// If kind: Status
	Status status `json:"status"`
	// If kind: Services
	Spec serviceSpec `json:"spec"`
}

type status struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Code    int    `json:"code"`
}

type metadata struct {
	Name            string            `json:"name"` // Service name
	ResourceVersion string            `json:"resourceVersion"`
	Namespace       string            `json:"namespace"` // Namespace where service is sitting.
	Annotations     map[string]string `json:"annotations"`
}

type serviceSpec struct {
	Ports []portSpec `json:"ports"`
}

type portSpec struct {
	// What pod port to get.
	TargetPort interface{} `json:"targetPort"` // uint32 / string
	Name       string      `json:"name"`
	// Expose port.
	Port uint32 `json:"port"`
}

// proxyAllEvents gets events in loop and proxies to eventsCh. If event include some error it always returns, because
// errors are meant to irrecoverable.
func proxyAllEvents(ctx context.Context, decoder *json.Decoder, eventsCh chan<- watchResult) {
	for ctx.Err() == nil {
		var eventErr error
		var got event
		// Blocking read.
		if err := decoder.Decode(&got); err != nil {
			if ctx.Err() != nil {
				// Stopping state.
				return
			}
			switch err {
			case io.EOF:
				eventErr = io.EOF
			case io.ErrUnexpectedEOF:
				eventErr = errors.Wrap(err, "Unexpected EOF during watch stream event decoding")
			default:
				eventErr = errors.Wrap(err, "Unable to decode an event from the watch stream")
			}
		}

		if eventErr == nil {
			switch got.Type {
			case added, modified, deleted:
			// All is fine.
			case failed:
				eventErr = errors.Errorf("%s: %s. Code: %d",
					got.Object.Status.Status,
					got.Object.Status.Message,
					got.Object.Status.Code,
				)
			default:
				eventErr = errors.Errorf("Got invalid watch event type: %v", got.Type)
			}
		}

		eventsCh <- watchResult{
			ep:  &got,
			err: eventErr,
		}
		if eventErr != nil {
			// Error is irrecoverable for watcher.Next(). Return here.
			return
		}
	}
}
