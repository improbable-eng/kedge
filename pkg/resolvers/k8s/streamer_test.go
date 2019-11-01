package k8sresolver

import (
	"context"
	"io"
	"testing"
	"time"

	"encoding/json"

	"github.com/fortytw2/leaktest"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

type readerCloserMock struct {
	ctx    context.Context
	cancel context.CancelFunc

	bytesCh <-chan []byte
	errCh   <-chan error
}

func (m *readerCloserMock) Read(p []byte) (n int, err error) {
	select {
	case <-m.ctx.Done():
		return 0, m.ctx.Err()
	case chunk := <-m.bytesCh:
		n = copy(p, chunk)
		return n, nil
	case err := <-m.errCh:
		return 0, err
	}
}

func (m *readerCloserMock) Close() error {
	m.cancel()
	return nil
}

type endpointClientMock struct {
	t       *testing.T
	started bool
	connCtx context.Context

	expectedTarget          targetEntry
	expectedResourceVersion int

	bytesCh chan []byte
	errCh   chan error
}

func (m *endpointClientMock) StartChangeStream(ctx context.Context, t targetEntry) (io.ReadCloser, error) {
	require.Equal(m.t, m.expectedTarget, t)
	require.False(m.t, m.started)
	m.started = true

	innerCtx, cancel := context.WithCancel(ctx)
	m.connCtx = innerCtx
	return &readerCloserMock{
		ctx:    innerCtx,
		cancel: cancel,

		bytesCh: m.bytesCh,
		errCh:   m.errCh,
	}, nil
}

func startTestStream(t *testing.T) (*endpointClientMock, *streamer) {
	testTarget := targetEntry{
		service:   "service1",
		port:      noTargetPort,
		namespace: "namespace1",
	}

	epClientMock := &endpointClientMock{
		t:              t,
		expectedTarget: testTarget,
		bytesCh:        make(chan []byte),
		errCh:          make(chan error),
	}

	s, err := startNewStreamer(testTarget, epClientMock)
	if err != nil {
		t.Fatal(err.Error())
	}
	return epClientMock, s
}

func TestStreamWatcher_DecodingError_EventErr_ClosesStream(t *testing.T) {
	defer leaktest.CheckTimeout(t, 10*time.Second)

	clientMock, streamer := startTestStream(t)
	defer streamer.Close()

	changeCh, errCh := streamer.ResultChans()

	// Triggering error while decoding.
	// It should block on connMock.Read by now.
	// Send some undecodable stuff:
	clientMock.bytesCh <- []byte(`{{{{ "temp-err": true}`)
	select {
	case err := <-errCh:
		require.Error(t, err, "we expect it fails to decode changeCh")
	case change := <-changeCh:
		t.Errorf("unexpected change event %v", change)
		t.FailNow()
	}

	// Error on streamer, so expect connection closed.
	select {
	case <-errCh:
		t.Error("No err was expected")
	case <-changeCh:
		t.Error("No event was expected")
	// Not really nice to use time in tests, but should be enough for now.
	case <-time.After(200 * time.Millisecond):
	}
	require.Error(t, clientMock.connCtx.Err())
}

func TestStreamWatcher_NotSupportedType_EventErr_ClosesConn(t *testing.T) {
	defer leaktest.CheckTimeout(t, 10*time.Second)

	clientMock, streamer := startTestStream(t)
	defer streamer.Close()

	changeCh, errCh := streamer.ResultChans()

	// Triggering not supported event.
	wrongEvent := event{
		Type: "not-supported",
	}
	b, err := json.Marshal(wrongEvent)
	require.NoError(t, err)

	clientMock.bytesCh <- []byte(b)
	select {
	case err := <-errCh:
		require.Error(t, err, "we expect invalid event type error")
		require.Equal(t, "got invalid watch event type: not-supported", err.Error())
	case change := <-changeCh:
		t.Errorf("unexpected change event %v", change)
		t.FailNow()
	}

	// Error on streamer, so expect connection closed.
	select {
	case <-errCh:
		t.Error("No err was expected")
	case <-changeCh:
		t.Error("No event was expected")
		// Not really nice to use time in tests, but should be enough for now.
	case <-time.After(200 * time.Millisecond):
	}
	require.Error(t, clientMock.connCtx.Err())
}

func TestStreamWatcher_ErrorEventType_EventErr_ClosesConn(t *testing.T) {
	defer leaktest.CheckTimeout(t, 10*time.Second)

	clientMock, streamer := startTestStream(t)
	defer streamer.Close()

	changeCh, errCh := streamer.ResultChans()

	status := &metav1.Status{
		Status:  "err",
		Code:    124,
		Message: "some-msg",
	}
	// Triggering not supported event.
	wrongEvent := event{
		Type: watch.Error,
		Object: eventObject{
			Status: status,
		},
	}
	b, err := json.Marshal(wrongEvent)
	require.NoError(t, err)

	clientMock.bytesCh <- []byte(b)
	select {
	case err := <-errCh:
		require.Error(t, err, "we expect event type error")
		require.Equal(t, "err: some-msg. Code: 124", err.Error())
	case change := <-changeCh:
		t.Errorf("unexpected change event %v", change)
		t.FailNow()
	}

	// Error on streamer, so expect connection closed.
	select {
	case <-errCh:
		t.Error("No err was expected")
	case <-changeCh:
		t.Error("No event was expected")
		// Not really nice to use time in tests, but should be enough for now.
	case <-time.After(200 * time.Millisecond):
	}
	require.Error(t, clientMock.connCtx.Err())
}

func TestStreamWatcher_EOF_EventErr_ClosesConn(t *testing.T) {
	defer leaktest.CheckTimeout(t, 10*time.Second)

	clientMock, streamer := startTestStream(t)
	defer streamer.Close()

	changeCh, errCh := streamer.ResultChans()

	// Triggering EOF.
	clientMock.errCh <- io.EOF
	select {
	case err := <-errCh:
		require.Error(t, err, "we expect EOF error")
		require.Equal(t, err, io.EOF)
	case change := <-changeCh:
		t.Errorf("unexpected change event %v", change)
		t.FailNow()
	}

	// Error on streamer, so expect connection closed.
	select {
	case <-errCh:
		t.Error("No err was expected")
	case <-changeCh:
		t.Error("No event was expected")
		// Not really nice to use time in tests, but should be enough for now.
	case <-time.After(200 * time.Millisecond):
	}
	require.Error(t, clientMock.connCtx.Err())
}

func newTestChange(typ watch.EventType, subs ...v1.EndpointSubset) change {
	return change{
		typ: typ,
		Endpoints: &v1.Endpoints{
			Subsets: subs,
		},
	}
}

func changeToEvent(change change) event {
	return event{
		Type: change.typ,
		Object: eventObject{
			Endpoints: change.Endpoints,
		},
	}
}

func TestStreamWatcher_OK_TwoOKEvents(t *testing.T) {
	defer leaktest.CheckTimeout(t, 10*time.Second)

	clientMock, streamer := startTestStream(t)
	defer streamer.Close()

	changeCh, errCh := streamer.ResultChans()

	// Triggering OK event..
	expectedChange := newTestChange(watch.Added, testAddr1)
	b, err := json.Marshal(changeToEvent(expectedChange))
	require.NoError(t, err)
	clientMock.bytesCh <- b

	select {
	case err := <-errCh:
		t.Errorf("unexpected error event: %v", err)
		t.FailNow()
	case change := <-changeCh:
		require.Equal(t, expectedChange, change)
	}

	expectedChange2 := newTestChange(watch.Deleted, testAddr1)
	b2, err2 := json.Marshal(changeToEvent(expectedChange2))
	require.NoError(t, err2)
	clientMock.bytesCh <- b2

	select {
	case err := <-errCh:
		t.Errorf("unexpected error event %v", err)
		t.FailNow()
	case change := <-changeCh:
		require.Equal(t, expectedChange2, change)
	}
}
