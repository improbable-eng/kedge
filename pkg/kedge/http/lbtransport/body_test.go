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
			name: "WrappedNil_Read_ShouldReturnEOF",
			src:                 nil,
			sequentialReadBytes: []int{10},
			rewindBeforeRead:    []bool{false},

			expectedBytes: [][]byte{{}},
			expectedErrs:  []error{io.EOF},
		},
		{
			name: "WrappedNil_RewindRead_ShouldReturnEOF",
			src:                 nil,
			sequentialReadBytes: []int{10},
			rewindBeforeRead:    []bool{true},

			expectedBytes: [][]byte{{}},
			expectedErrs:  []error{io.EOF},
		},
		{
			name: "SmallBigBigReads_FinishedWithEOF",
			src:                 bytes.NewReader([]byte{1, 2, 3, 4}),
			sequentialReadBytes: []int{1, 8192, 8192},
			rewindBeforeRead:    []bool{false, false, false},

			expectedBytes: [][]byte{{1}, {2, 3, 4}, {}},
			expectedErrs:  []error{nil, nil, io.EOF},
		},
		{
			name: "SmallReads_FinishedWithEOF",
			src:                 bytes.NewReader([]byte{1, 2, 3, 4}),
			sequentialReadBytes: []int{1, 2, 4, 1},
			rewindBeforeRead:    []bool{false, false, false, false},

			expectedBytes: [][]byte{{1}, {2, 3}, {4}, {}},
			expectedErrs:  []error{nil, nil, nil, io.EOF},
		},
		{
			name: "SmallReadsTakingExactBytes",
			src:                 bytes.NewReader([]byte{1, 2, 3, 4, 5}),
			sequentialReadBytes: []int{1, 2, 2},
			rewindBeforeRead:    []bool{false, false, false},

			expectedBytes: [][]byte{{1}, {2, 3}, {4, 5}},
			expectedErrs:  []error{nil, nil, nil},
		},
		{
			name: "SmallReadsRewindSmallRead",
			src:                 bytes.NewReader([]byte{1, 2, 3, 4, 5}),
			sequentialReadBytes: []int{1, 2, 4, 2},
			rewindBeforeRead:    []bool{false, false, true, false},

			expectedBytes: [][]byte{{1}, {2, 3}, {1, 2, 3, 4}, {5}},
			expectedErrs:  []error{nil, nil, nil, nil},
		},
		{
			name: "BigReadRewindSmallReads",
			src:                 bytes.NewReader([]byte{1, 2, 3, 4}),
			sequentialReadBytes: []int{8192, 2, 3},
			rewindBeforeRead:    []bool{false, true, false},

			expectedBytes: [][]byte{{1, 2, 3, 4}, {1, 2}, {3, 4}},
			expectedErrs:  []error{nil, nil, nil},
		},
		{
			name: "BigReadRewindBigReadSmall_FinishedWithEOF",
			src:                 bytes.NewReader([]byte{1, 2, 3, 4}),
			sequentialReadBytes: []int{8192, 8192, 3},
			rewindBeforeRead:    []bool{false, true, false},

			expectedBytes: [][]byte{{1, 2, 3, 4}, {1, 2, 3, 4}, {}},
			expectedErrs:  []error{nil, nil, io.EOF},
		},
	} {
		if ok := t.Run(tcase.name, func(t *testing.T) {
			b := newReplayableReader(tcase.src)

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
