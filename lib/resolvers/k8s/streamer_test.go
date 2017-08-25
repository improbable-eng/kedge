package k8sresolver

import (
	"context"
	"encoding/json"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"fmt"
	"strings"
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

func (m *readerCloserMock) isClosed() bool {
	isClosed, ok := m.IsClosed.Load().(bool)
	if ok {
		return isClosed
	}
	return false
}

type endpointClientMock struct {
	t *testing.T

	expectedTarget          targetEntry
	expectedResourceVersion int

	connMock *readerCloserMock
}

func (m *endpointClientMock) StartChangeStream(ctx context.Context, t targetEntry) (io.ReadCloser, error) {
	require.Equal(m.t, m.expectedTarget, t)
	return m.connMock, nil
}

func startTestStream(t *testing.T) (chan []byte, chan error, *readerCloserMock, chan watchResult, func()) {
	bytesCh := make(chan []byte)
	errCh := make(chan error)
	ctx, cancel := context.WithCancel(context.TODO())

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
	closeFn := func() {
		cancel()
		cancel2()
	}

	eventsCh := make(chan watchResult)

	err := startWatchingEndpointsChanges(
		streamWatcherCtx,
		testTarget,
		epClientMock,
		eventsCh,
	)
	if err != nil {
		closeFn()
		t.Fatal(err.Error())
	}

	return bytesCh, errCh, connMock, eventsCh, closeFn
}

func TestStreamWatcher_DecodingError_EventErr_ClosesStream(t *testing.T) {
	bytesCh, _, connMock, eventsCh, cancel := startTestStream(t)
	defer cancel()

	// Triggering error while decoding.
	// It should block on connMock.Read by now.
	// Send some undecodable stuff:
	bytesCh <- []byte(`{{{{ "temp-err": true}`)
	event := <-eventsCh
	require.Error(t, event.err, "we expect it fails to decode eventCh")

	// Expect connection closed.
	select {
	case <-eventsCh:
		t.Error("No event was expected")
	// Not really nice to use time in tests, but should be enough for now.
	case <-time.After(200 * time.Millisecond):
	}
	require.True(t, connMock.isClosed())
}

func TestStreamWatcher_NotSupportedType_EventErr_ClosesConn(t *testing.T) {
	bytesCh, _, connMock, eventsCh, cancel := startTestStream(t)
	defer cancel()

	// Triggering not supported event.
	wrongEvent := event{
		Type: "not-supported",
	}
	b, err := json.Marshal(wrongEvent)
	require.NoError(t, err)

	bytesCh <- []byte(b)
	event := <-eventsCh
	require.Error(t, event.err, "we expect it invalid event type error")

	// Expect connection closed.
	select {
	case <-eventsCh:
		t.Error("No event was expected")
	// Not really nice to use time in tests, but should be enough for now.
	case <-time.After(200 * time.Millisecond):
	}
	require.True(t, connMock.isClosed())
}

func TestStreamWatcher_EOF_EventErr_ClosesConn(t *testing.T) {
	_, errCh, connMock, eventsCh, cancel := startTestStream(t)
	defer cancel()

	// Triggering EOF.
	errCh <- io.EOF
	event := <-eventsCh
	require.Error(t, event.err, "we expect it invalid event type error")

	// Expect connection closed.
	select {
	case <-eventsCh:
		t.Error("No event was expected")
	// Not really nice to use time in tests, but should be enough for now.
	case <-time.After(200 * time.Millisecond):
	}
	require.True(t, connMock.isClosed())
}

func TestStreamWatcher_OK_2OKEvents(t *testing.T) {
	bytesCh, _, _, eventsCh, cancel := startTestStream(t)
	defer cancel()

	// Triggering OK gotEvent.
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

	b, err := json.Marshal(expectedEvent)
	require.NoError(t, err)
	bytesCh <- b
	gotEvent := <-eventsCh
	require.NoError(t, gotEvent.err)
	require.Equal(t, expectedEvent, *gotEvent.ep)

	expectedEvent2 := event{
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

	b2, err2 := json.Marshal(expectedEvent2)
	fmt.Println(string(b2))
	require.NoError(t, err2)
	bytesCh <- b2
	gotEvent2 := <-eventsCh
	require.NoError(t, gotEvent2.err)
	require.Equal(t, expectedEvent2, *gotEvent2.ep)
}
