package lbtransport

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReplayableReader(t *testing.T) {
	for _, tcase := range []struct {
		name string
		src                 io.Reader
		sequentialReadBytes []int
		rewindBeforeRead    []bool

		expectedBytes [][]byte
		expectedErrs  []error
	}{
		{
			name: "wrapped nil, read should return EOF",
			src:                 nil,
			sequentialReadBytes: []int{10},
			rewindBeforeRead:    []bool{false},

			expectedBytes: [][]byte{{}},
			expectedErrs:  []error{io.EOF},
		},
		{
			name: "wrapped nil, read should return EOF after rewind",
			src:                 nil,
			sequentialReadBytes: []int{10},
			rewindBeforeRead:    []bool{true},

			expectedBytes: [][]byte{{}},
			expectedErrs:  []error{io.EOF},
		},
		{
			name: "small read and two big reads",
			src:                 bytes.NewReader([]byte{1, 2, 3, 4}),
			sequentialReadBytes: []int{1, 8192, 8192},
			rewindBeforeRead:    []bool{false, false, false},

			expectedBytes: [][]byte{{1}, {2, 3, 4}, {}},
			expectedErrs:  []error{nil, nil, io.EOF},
		},
		{
			name: "small reads only",
			src:                 bytes.NewReader([]byte{1, 2, 3, 4}),
			sequentialReadBytes: []int{1, 2, 4, 1},
			rewindBeforeRead:    []bool{false, false, false, false},

			expectedBytes: [][]byte{{1}, {2, 3}, {4}, {}},
			expectedErrs:  []error{nil, nil, nil, io.EOF},
		},
		{
			name: "small reads taking exactly all bytes in total",
			src:                 bytes.NewReader([]byte{1, 2, 3, 4, 5}),
			sequentialReadBytes: []int{1, 2, 2},
			rewindBeforeRead:    []bool{false, false, false},

			expectedBytes: [][]byte{{1}, {2, 3}, {4, 5}},
			expectedErrs:  []error{nil, nil, nil},
		},
		{
			name: "2 small reads, rewind and read",
			src:                 bytes.NewReader([]byte{1, 2, 3, 4, 5}),
			sequentialReadBytes: []int{1, 2, 4, 2},
			rewindBeforeRead:    []bool{false, false, true, false},

			expectedBytes: [][]byte{{1}, {2, 3}, {1, 2, 3, 4}, {5}},
			expectedErrs:  []error{nil, nil, nil, nil},
		},
		{
			name: "big read, rewind and small reads",
			src:                 bytes.NewReader([]byte{1, 2, 3, 4}),
			sequentialReadBytes: []int{8192, 2, 3},
			rewindBeforeRead:    []bool{false, true, false},

			expectedBytes: [][]byte{{1, 2, 3, 4}, {1, 2}, {3, 4}},
			expectedErrs:  []error{nil, nil, nil},
		},
		{
			name: "big read, rewind and big read and small",
			src:                 bytes.NewReader([]byte{1, 2, 3, 4}),
			sequentialReadBytes: []int{8192, 8192, 3},
			rewindBeforeRead:    []bool{false, true, false},

			expectedBytes: [][]byte{{1, 2, 3, 4}, {1, 2, 3, 4}, {}},
			expectedErrs:  []error{nil, nil, io.EOF},
		},
	} {
		if ok := t.Run(tcase.name, func(t *testing.T) {
			b := newLazyBufferedReader(tcase.src)

			for i, read := range tcase.sequentialReadBytes {
				if tcase.rewindBeforeRead[i] {
					b.rewind()
				}
				toRead := make([]byte, read)

				n, err := b.Read(toRead)
				require.Equal(t, tcase.expectedErrs[i], err, "read %d", i+1)
				require.Len(t, tcase.expectedBytes[i], n, "read %d", i+1)
				require.Equal(t, tcase.expectedBytes[i], toRead[:len(tcase.expectedBytes[i])], "read %d", i+1)
			}

		}); !ok {
			return
		}
	}
}
