package logstash

import (
	"net"
	"testing"
	"time"

	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/pkg/errors"
)

func TestReconnectingWriter_WriteDoesNotReturnUntilWriteIsSuccessful(t *testing.T) {
	writerWithError := &mockWriterCloser{errOnWrite: errors.New("some expected error")}
	writerWithoutError := &mockWriterCloser{}
	dialReturns := []net.Conn{
		writerWithError,
		writerWithoutError,
	}
	callCount := 0

	dial := func() (net.Conn, error) {
		if callCount >= len(dialReturns) {
			return nil, errors.New("called more than call count")
		}
		ret := dialReturns[callCount]
		callCount++
		return ret, nil
	}

	errLogger, _ := test.NewNullLogger()
	writer := NewReconnectingWriter(dial, errLogger)

	var byteCount int
	var err error
	writeDone := make(chan struct{})
	body := []byte("some random data")
	go func() {
		byteCount, err = writer.Write(body)
		close(writeDone)
	}()

	select {
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for write to complete")
	case <-writeDone:
	}

	assert.Equal(t, 1, writerWithError.writeCallCount)
	assert.Equal(t, 1, writerWithoutError.writeCallCount)
	// verifies that the write is completed using the new writer
	assert.Equal(t, len(body), byteCount)
	assert.NoError(t, err, "must have successfully written a log after redial")
}

type mockWriterCloser struct {
	writeCallCount int
	errOnWrite     error
	errOnClose     error
}

func (w *mockWriterCloser) Write(body []byte) (int, error) {
	w.writeCallCount += 1
	if w.errOnWrite != nil {
		return 0, w.errOnWrite
	}
	return len(body), nil
}

func (w *mockWriterCloser) Close() error {
	return w.errOnClose
}

func (w *mockWriterCloser) Read(b []byte) (n int, err error) {
	panic("implement me")
}

func (w *mockWriterCloser) LocalAddr() net.Addr {
	panic("implement me")
}

func (w *mockWriterCloser) RemoteAddr() net.Addr {
	panic("implement me")
}

func (w *mockWriterCloser) SetDeadline(t time.Time) error {
	panic("implement me")
}

func (w *mockWriterCloser) SetReadDeadline(t time.Time) error {
	panic("implement me")
}

func (w *mockWriterCloser) SetWriteDeadline(t time.Time) error {
	return nil
}
