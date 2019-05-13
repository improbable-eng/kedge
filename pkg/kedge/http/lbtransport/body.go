package lbtransport

import (
	"bytes"
	"io"
)

type lazyBufferedReader struct {
	wrapped io.Reader
	buf     []byte
	offset  int
}

func (*lazyBufferedReader) Close() error {
	return nil
}

// rewind allows lazyBufferedReader to be read again.
func (b *lazyBufferedReader) rewind() {
	if b == nil {
		return
	}
	b.offset = 0
}

func (b *lazyBufferedReader) Read(p []byte) (n int, err error) {
	if b == nil {
		return 0, io.EOF
	}
	defer func() { b.offset += n }()

	if len(b.buf)-b.offset > 0 {
		n, err = bytes.NewReader(b.buf).Read(p)
		if err != nil {
			return n, err
		}
		if n == len(p) {
			return n, nil
		}
	}

	tmp := bytes.NewBuffer(b.buf)
	n64, err := tmp.ReadFrom(io.LimitReader(b.wrapped, int64(len(p)-n)))
	defer func() { n += int(n64) }()

	if n64 == 0 {
		return 0, io.EOF
	}
	b.buf = tmp.Bytes()
	copy(p[n:], b.buf[b.offset+n:])
	return n, err
}

// newLazyBufferedReader returns lazyBufferedReader that lazely read from underlying reader if needed.
// In worst case scenario all content from src is buffered, in the the best, nothing.
func newLazyBufferedReader(src io.Reader) *lazyBufferedReader {
	if src == nil {
		return nil
	}

	return &lazyBufferedReader{wrapped: src}
}
