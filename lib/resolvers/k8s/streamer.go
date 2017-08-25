package k8sresolver

import (
	"context"
	"encoding/json"
	"io"

	"github.com/pkg/errors"
)

// startWatchingEndpointsChanges starts a stream that in go routine reads from connection for every change event.
// Since watcher.Next() errors are assumed irrecoverable, it is a caller responsibility to re-resolve on EOF, error event etc.
// We read connection from separate go routine because read is blocking with no timeout/cancel logic.
func startWatchingEndpointsChanges(
	ctx context.Context,
	target targetEntry,
	epClient endpointClient,
	eventsCh chan<- watchResult,
) error {
	stream, err := epClient.StartChangeStream(ctx, target)
	if err != nil {
		return errors.Wrapf(err, "k8sresolver stream: Failed to do start stream for target %s", target)
	}

	go func() {
		select {
		case <-ctx.Done():
			stream.Close()
		}
	}()

	go func() {
		proxyAllEvents(ctx, json.NewDecoder(stream), eventsCh)
		stream.Close()
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
	Object endpoints `json:"object"`
}

// proxyAllEvents gets events in loop and proxies to eventsCh. If event include some error it always returns, because
// watchers.Next errors are meant to irrecoverable.
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
				// Watch closed normally - weird.
				eventErr = errors.Wrap(err, "EOF during watch stream event decoding")
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
					got.Object.Status,
					got.Object.Message,
					got.Object.Code,
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
