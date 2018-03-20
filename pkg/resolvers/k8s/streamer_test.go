package k8sresolver

import (
	"context"
	"encoding/json"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	"github.com/stretchr/testify/require"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
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
	m.connMock.Ctx = ctx
	return m.connMock, nil
}

func startTestStream(t *testing.T) (chan []byte, chan error, *readerCloserMock, *streamer, func()) {
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

	s, err := startNewStreamer(streamWatcherCtx, testTarget, epClientMock)
	if err != nil {
		closeFn()
		t.Fatal(err.Error())
	}

	return bytesCh, errCh, connMock, s, closeFn
}

func TestStreamWatcher_DecodingError_EventErr_ClosesStream(t *testing.T) {
	defer leaktest.CheckTimeout(t, 10*time.Second)

	bytesCh, _, connMock, streamer, cancel := startTestStream(t)
	defer cancel()

	changeCh, errCh := streamer.ResultChans()

	// Triggering error while decoding.
	// It should block on connMock.Read by now.
	// Send some undecodable stuff:
	bytesCh <- []byte(`{{{{ "temp-err": true}`)
	select {
	case err := <-errCh:
		require.Error(t, err, "we expect it fails to decode changeCh")
	case change := <-changeCh:
		t.Errorf("unexpected change event %v", change)
		t.FailNow()
	}

	// Expect connection closed.
	select {
	case <-errCh:
		t.Error("No err was expected")
	case <-changeCh:
		t.Error("No event was expected")
	// Not really nice to use time in tests, but should be enough for now.
	case <-time.After(200 * time.Millisecond):
	}
	require.True(t, connMock.isClosed())
}

func TestStreamWatcher_NotSupportedType_EventErr_ClosesConn(t *testing.T) {
	defer leaktest.CheckTimeout(t, 10*time.Second)

	bytesCh, _, connMock, streamer, cancel := startTestStream(t)
	defer cancel()

	changeCh, errCh := streamer.ResultChans()

	// Triggering not supported event.
	wrongEvent := event{
		Type: "not-supported",
	}
	b, err := json.Marshal(wrongEvent)
	require.NoError(t, err)

	bytesCh <- []byte(b)
	select {
	case err := <-errCh:
		require.Error(t, err, "we expect invalid event type error")
		require.Equal(t, "got invalid watch event type: not-supported", err.Error())
	case change := <-changeCh:
		t.Errorf("unexpected change event %v", change)
		t.FailNow()
	}

	// Expect connection closed.
	select {
	case <-errCh:
		t.Error("No err was expected")
	case <-changeCh:
		t.Error("No event was expected")
	// Not really nice to use time in tests, but should be enough for now.
	case <-time.After(200 * time.Millisecond):
	}
	require.True(t, connMock.isClosed())
}

func TestStreamWatcher_ErrorEventType_EventErr_ClosesConn(t *testing.T) {
	defer leaktest.CheckTimeout(t, 10*time.Second)

	bytesCh, _, connMock, streamer, cancel := startTestStream(t)
	defer cancel()

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

	bytesCh <- []byte(b)
	select {
	case err := <-errCh:
		require.Error(t, err, "we expect event type error")
		require.Equal(t, "err: some-msg. Code: 124", err.Error())
	case change := <-changeCh:
		t.Errorf("unexpected change event %v", change)
		t.FailNow()
	}

	// Expect connection closed.
	select {
	case <-errCh:
		t.Error("No err was expected")
	case <-changeCh:
		t.Error("No event was expected")
	// Not really nice to use time in tests, but should be enough for now.
	case <-time.After(200 * time.Millisecond):
	}
	require.True(t, connMock.isClosed())
}

func TestStreamWatcher_EOF_EventErr_ClosesConn(t *testing.T) {
	defer leaktest.CheckTimeout(t, 10*time.Second)

	_, connErrCh, connMock, streamer, cancel := startTestStream(t)
	defer cancel()

	changeCh, errCh := streamer.ResultChans()

	// Triggering EOF.
	connErrCh <- io.EOF
	select {
	case err := <-errCh:
		require.Error(t, err, "we expect EOF error")
		require.Equal(t, err, io.EOF)
	case change := <-changeCh:
		t.Errorf("unexpected change event %v", change)
		t.FailNow()
	}

	// Expect connection closed.
	select {
	case <-errCh:
		t.Error("No err was expected")
	case <-changeCh:
		t.Error("No event was expected")
	// Not really nice to use time in tests, but should be enough for now.
	case <-time.After(200 * time.Millisecond):
	}
	require.True(t, connMock.isClosed())
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

	bytesCh, _, _, streamer, cancel := startTestStream(t)
	defer cancel()

	changeCh, errCh := streamer.ResultChans()

	// Triggering OK event..
	expectedChange := newTestChange(watch.Added, testAddr1)
	b, err := json.Marshal(changeToEvent(expectedChange))
	require.NoError(t, err)
	bytesCh <- b

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
	bytesCh <- b2

	select {
	case err := <-errCh:
		t.Errorf("unexpected error event %v", err)
		t.FailNow()
	case change := <-changeCh:
		require.Equal(t, expectedChange2, change)
	}
}
