package k8sresolver

import (
	"context"
	"encoding/json"
	"io"
	"io/ioutil"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

type change struct {
	*v1.Endpoints
	typ watch.EventType
}

type streamer struct {
	changeCh chan change
	errCh    chan error
}

// startNewStreamer starts a stream that in go routine reads from connection for every change event.
// Since watcher.Next() errors are assumed irrecoverable, it is a caller responsibility to re-resolve on EOF, error event etc.
// We read connection from separate go routine because read is blocking with no timeout/cancel logic.
func startNewStreamer(ctx context.Context, target targetEntry, epClient endpointClient) (*streamer, error) {
	innerCtx, innerCancel := context.WithCancel(ctx)
	stream, err := epClient.StartChangeStream(innerCtx, target)
	if err != nil {
		innerCancel()
		return nil, errors.Wrapf(err, "Failed to do start stream for target %v", target)
	}

	go func() {
		select {
		case <-innerCtx.Done():
			// Request is cancelled, so we need to read what is left there to not leak go routines.
			_, _ = ioutil.ReadAll(stream)
			err = stream.Close()
			if err != nil {
				logrus.WithError(err).Warn("Failed to Close cancelled stream connection")
			}
		}
	}()
	s := &streamer{
		changeCh: make(chan change),
		errCh:    make(chan error, 1),
	}
	go func() {
		defer innerCancel()

		if err := proxy(innerCtx, json.NewDecoder(stream), s.changeCh); err != nil {
			s.errCh <- err
		}
	}()

	return s, nil
}

func (s *streamer) ResultChans() (<-chan change, <-chan error) {
	return s.changeCh, s.errCh
}

// We don't want to use special, internal decoder, so we need to have all typed.
type event struct {
	Type   watch.EventType
	Object eventObject
}

type eventObject struct {
	// If type == Error.
	*metav1.Status
	// If type == Added, Modified or Deleted.
	*v1.Endpoints
}

// proxy receives events in loop and proxies to changeCh. If event include some error, or stream errors, it always returns
// error. This is because watchers.Next errors are meant to irrecoverable. On cancelled context, no error is returned.
func proxy(ctx context.Context, decoder *json.Decoder, endpCh chan<- change) error {
	var event event

	for ctx.Err() == nil {
		// Blocking read.
		if err := decoder.Decode(&event); err != nil {
			if ctx.Err() != nil {
				// Stopping state.
				return nil
			}
			switch err {
			case io.EOF:
				// Stream closed gracefully.
				return io.EOF
			case io.ErrUnexpectedEOF:
				return errors.Wrap(err, "unexpected EOF during watch stream event decoding")
			default:
				return errors.Wrap(err, "unable to decode an event from the watch stream")
			}
		}

		switch event.Type {
		case watch.Error:
			if event.Object.Status != nil {
				return errors.Errorf("%s: %s. Code: %d",
					event.Object.Status.Status,
					event.Object.Status.Message,
					event.Object.Status.Code,
				)
			}
			return errors.Errorf("unexpected error object type %v", event.Object)

		case watch.Added, watch.Modified, watch.Deleted:
		default:
			return errors.Errorf("got invalid watch event type: %v", event.Type)
		}

		if event.Object.Endpoints == nil {
			return errors.Errorf("unexpected event object type %v. Expected *v1.Endpoints", event.Object)
		}
		endpCh <- change{
			Endpoints: event.Object.Endpoints,
			typ:       event.Type,
		}
	}
	return nil
}
