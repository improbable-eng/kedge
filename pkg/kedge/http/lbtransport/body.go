package lbtransport

import (
	"bytes"
	"io"
)

type replayableReader struct {
	wrapped io.Reader
	buf     []byte
	offset  int
}

func (*replayableReader) Close() error {
	return nil
}

// rewind allows replayableReader to be read again.
func (b *replayableReader) rewind() {
	if b == nil {
		return
	}
	b.offset = 0
}

func (b *replayableReader) Read(p []byte) (n int, err error) {
	if b == nil {
		return 0, io.EOF
	}

	if len(b.buf)-b.offset > 0 {
		n, err = bytes.NewReader(b.buf[b.offset:]).Read(p)
		b.offset += n
	}

	if err == nil && n < len(p) {
		var n64 int64

		// Try to buffer rest (if needed) from wrapped io.Reader.
		tmp := bytes.NewBuffer(b.buf)
		n64, err = tmp.ReadFrom(io.LimitReader(b.wrapped, int64(len(p)-n)))
		b.buf = tmp.Bytes()
		if n64 > 0 {
			copy(p[n:], b.buf[b.offset:])
			n += int(n64)
			b.offset += int(n64)
		}
	}

	// Buffer.ReadFrom masks io.EOF so we assume EOF once n == 0 and no error.
	if err == nil && n == 0 && len(p) > 0 {
		return 0, io.EOF
	}
	return n, err
}

// newLazyBufferedReader returns replayableReader that lazely read from underlying reader if needed.
// In worst case scenario all content from src is buffered, in the the best, nothing.
func newLazyBufferedReader(src io.Reader) *replayableReader {
	if src == nil {
		return nil
	}
	return &replayableReader{wrapped: src}
}
