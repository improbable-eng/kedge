package k8sresolver

import (
	"context"
	"encoding/json"
	"io"
	"strconv"
	"time"

	"github.com/jpillora/backoff"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type streamWatcher struct {
	logger                  logrus.FieldLogger
	target                  targetEntry
	epClient                endpointClient
	eventsCh                chan<- watchResult
	retryBackoff            *backoff.Backoff
	lastSeenResourceVersion int
}

func startWatchingEndpointsChanges(
	ctx context.Context,
	logger logrus.FieldLogger,
	target targetEntry,
	epClient endpointClient,
	eventsCh chan<- watchResult,
	retryBackoff *backoff.Backoff,
	lastSeenResourceVersion int,
) *streamWatcher {
	w := &streamWatcher{
		logger:                  logger,
		target:                  target,
		epClient:                epClient,
		eventsCh:                eventsCh,
		retryBackoff:            retryBackoff,
		lastSeenResourceVersion: lastSeenResourceVersion,
	}
	go w.watch(ctx)
	return w
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

// watch starts a stream and reads connection for every change event. If connection is broken (and ctx is still valid)
// it retries the stream. We read connection from separate go routine because read is blocking with no timeout/cancel logic.
func (w *streamWatcher) watch(ctx context.Context) {
	// Retry stream loop.
	for ctx.Err() == nil {
		stream, err := w.epClient.StartChangeStream(ctx, w.target, w.lastSeenResourceVersion)
		if err != nil {
			w.logger.WithError(err).Error("k8sresolver stream: Failed to do start stream")
			time.Sleep(w.retryBackoff.Duration())

			// TODO(bplotka): On X retry on failed, consider returning failed to Next() via watchResult that we
			// cannot connect.
			continue
		}
		w.logger.Debug("Started new Watch Enpoints stream.")

		err = w.proxyEvents(ctx, json.NewDecoder(stream))
		stream.Close()
		if ctx.Err() != nil {
			return
		}

		if err != nil {
			w.logger.WithError(err).Error("k8sresolver stream: Error on read and proxy Events. Retrying")
		}
	}
}

// proxyEvents is blocking method that gets events in loop and on success proxies to eventsCh.
// It ends only when context is cancelled and/or stream is broken.
func (w *streamWatcher) proxyEvents(ctx context.Context, decoder *json.Decoder) error {
	connectionErrCh := make(chan error)
	go func() {
		defer close(connectionErrCh)

		for {
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
					connectionErrCh <- errors.Wrap(err, "EOF during watch stream event decoding")
					return
				case io.ErrUnexpectedEOF:
					connectionErrCh <- errors.Wrap(err, "Unexpected EOF during watch stream event decoding")
					return
				default:

				}
				// This is odd case. We return error as well as recreate stream.
				err := errors.Wrap(err, "Unable to decode an event from the watch stream")
				connectionErrCh <- err
				w.eventsCh <- watchResult{
					err: errors.Wrap(err, "Unable to decode an event from the watch stream"),
				}
				return
			}

			switch got.Type {
			case added, modified, deleted, failed:
				rv, err := strconv.Atoi(got.Object.Metadata.ResourceVersion)
				if err != nil {
					if got.Object.Metadata.ResourceVersion != "" {
						w.eventsCh <- watchResult{
							ep:  &got,
							err: err,
						}
						continue
					}
					w.logger.WithError(err).Error("ResourceVersion is empty for event type %s. Retrying with lastSeenResourceVersion + 1", got.Type)
					w.lastSeenResourceVersion += 1
				} else {
					w.lastSeenResourceVersion = rv
				}
				w.eventsCh <- watchResult{
					ep: &got,
				}
			default:
				w.eventsCh <- watchResult{
					err: errors.Errorf("Got invalid watch event type: %v", got.Type),
				}
			}
		}

	}()

	// Wait until context is done or connection ends.
	select {
	case <-ctx.Done():
		// Stopping state.
		return ctx.Err()
	case err := <-connectionErrCh:
		return err
	}
}
