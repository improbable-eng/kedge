package k8sresolver

import (
	"context"
	"encoding/json"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jpillora/backoff"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

type readerCloserMock struct {
	Ctx      context.Context
	BytesCh  <-chan []byte
	ErrCh    <-chan error
	IsClosed atomic.Value
}

func (m *readerCloserMock) Read(p []byte) (n int, err error) {
	select {
	case <-m.Ctx.Done():
		return 0, m.Ctx.Err()
	case chunk := <-m.BytesCh:
		n = copy(p, chunk)
		return n, nil
	case err := <-m.ErrCh:
		return 0, err
	}
}

func (m *readerCloserMock) Close() error {
	m.IsClosed.Store(true)
	return nil
}

type endpointClientMock struct {
	t *testing.T

	expectedTarget          targetEntry
	expectedResourceVersion int

	connMock   *readerCloserMock
	reconnects uint
}

func (m *endpointClientMock) StartChangeStream(ctx context.Context, t targetEntry, resourceVersion int) (io.ReadCloser, error) {
	m.reconnects++
	require.Equal(m.t, m.expectedTarget, t)
	return m.connMock, nil
}

func TestStreamWatcher(t *testing.T) {
	bytesCh := make(chan []byte)
	errCh := make(chan error)
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	connMock := &readerCloserMock{
		Ctx:     ctx,
		BytesCh: bytesCh,
		ErrCh:   errCh,
	}

	testTarget := targetEntry{
		service:   "service1",
		port:      noTargetPort,
		namespace: "namespace1",
	}

	epClientMock := &endpointClientMock{
		t:              t,
		expectedTarget: testTarget,
		connMock:       connMock,
	}

	streamWatcherCtx, cancel2 := context.WithCancel(ctx)
	defer cancel2()

	eventsCh := make(chan watchResult)

	startWatchingEndpointsChanges(
		streamWatcherCtx,
		logrus.New(),
		testTarget,
		epClientMock,
		eventsCh,
		&backoff.Backoff{Min: 10 * time.Millisecond, Max: 10 * time.Millisecond},
		0,
	)

	localReconnectCounter := uint(1)

	// Call number 1 - triggering error while decoding

	// It should block on connMock.Read by now.
	// Send some undecodable stuff:
	bytesCh <- []byte(`{{{{ "temp-err": true}`)
	eventCh := <-eventsCh
	require.Error(t, eventCh.err, "we expect it fails to decode eventCh")
	require.Nil(t, eventCh.ep)

	// This error should recreate stream
	localReconnectCounter++

	// Call number 2 - triggering not supported event.

	wrongEvent := event{
		Type: "not-supported",
	}
	b, err := json.Marshal(wrongEvent)
	require.NoError(t, err)

	bytesCh <- []byte(b)
	eventCh = <-eventsCh
	require.Error(t, eventCh.err, "we expect it invalid event type error")
	require.Nil(t, eventCh.ep)

	// Call number 3 - triggering EOF.

	errCh <- io.EOF
	// Just expect reconnect.
	select {
	case <-eventsCh:
		t.Error("No event was expected")
	// Not really nice to use time in tests, but should be enough for now.
	case <-time.After(200 * time.Millisecond):
	}

	// This error should recreate stream
	localReconnectCounter++

	// Call number 4 - triggering OK event.

	expectedEvent := event{
		Type: added,
		Object: endpoints{
			Metadata: metadata{
				ResourceVersion: "123",
			},
			Subsets: []subset{
				{
					Ports: []port{
						{
							Port: 8080,
							Name: "noName",
						},
					},
					Addresses: []address{
						{
							IP: "1.2.3.4",
						},
					},
				},
			},
		},
	}

	b, err = json.Marshal(expectedEvent)
	require.NoError(t, err)
	bytesCh <- b
	eventCh = <-eventsCh
	require.NoError(t, eventCh.err)
	require.Equal(t, expectedEvent, *eventCh.ep)

	require.Equal(t, localReconnectCounter, epClientMock.reconnects)
}
