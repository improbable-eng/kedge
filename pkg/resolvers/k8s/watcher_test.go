package k8sresolver

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/naming"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/watch"
)

var (
	testAddr1 = v1.EndpointSubset{
		Ports: []v1.EndpointPort{
			{
				Port: 8080,
			},
			{
				Port: 8081,
			},
			{
				Port: 8082,
			},
		},
		Addresses: []v1.EndpointAddress{
			{
				IP: "1.2.3.4",
			},
		},
	}
	modifiedAddr1 = v1.EndpointSubset{
		Ports: []v1.EndpointPort{
			{
				Port: 8080,
			},
		},
		Addresses: []v1.EndpointAddress{
			{
				IP: "1.2.3.5", // Changed IP.
			},
		},
	}
	multipleAddrSubset = v1.EndpointSubset{
		Ports: []v1.EndpointPort{
			{
				Port: 8080,
			},
		},
		Addresses: []v1.EndpointAddress{
			{
				IP: "1.2.3.3",
			},
			{
				IP: "1.2.4.4",
			},
			{
				IP: "1.2.5.5",
			},
		},
	}
	modifiedMultipleAddrSubset = v1.EndpointSubset{
		Ports: []v1.EndpointPort{
			{
				Port: 8080,
			},
		},
		Addresses: []v1.EndpointAddress{
			{
				IP: "1.2.3.3",
			},
			{
				IP: "1.2.4.5", // Changed
			},
			// IP: "1.2.5.5" deleted
		},
	}
)

func TestWatcher_Next_OK(t *testing.T) {
	for _, tcase := range []struct {
		watchedTargetPort targetPort
		changes           []change
		err               error
		expectedUpdates   [][]*naming.Update
		expectedErrs      []error
	}{
		// Tests for subsetToAddresses function.
		{
			watchedTargetPort: targetPort{},
			changes:           []change{newTestChange(watch.Added, testAddr1)},
			expectedErrs: []error{errors.New("failed to convert k8s endpoint subset to update Addr: we got " +
				"[{ 8080 } { 8081 } { 8082 }] ports and target port is not specified. Don't know what to choose")},
		},
		{
			watchedTargetPort: targetPort{},
			changes:           []change{newTestChange(watch.Added, modifiedAddr1)},
			expectedUpdates: [][]*naming.Update{
				{
					{
						Addr: "1.2.3.5:8080",
						Op:   naming.Add,
					},
				},
			},
		},
		{
			watchedTargetPort: targetPort{value: "8082"},
			changes:           []change{newTestChange(watch.Added, testAddr1)},
			expectedUpdates: [][]*naming.Update{
				{
					{
						Addr: "1.2.3.4:8082",
						Op:   naming.Add,
					},
				},
			},
		},
		{
			watchedTargetPort: targetPort{value: "8081"},
			changes:           []change{newTestChange(watch.Added, testAddr1)},
			expectedUpdates: [][]*naming.Update{
				{
					{
						Addr: "1.2.3.4:8081",
						Op:   naming.Add,
					},
				},
			},
		},
		{
			// Non existing port just return no IPs. This makes configuration bit harder to debug, but we cannot assume
			// port is always in any subset.
			watchedTargetPort: targetPort{value: "9999"},
			changes:           []change{newTestChange(watch.Added, testAddr1)},
			expectedUpdates:   [][]*naming.Update{nil},
		},
		// Watcher next() tests:
		{
			watchedTargetPort: targetPort{value: "8080"},
			changes: []change{
				newTestChange(watch.Added, testAddr1),
				newTestChange(watch.Modified, modifiedAddr1),
			},
			expectedUpdates: [][]*naming.Update{
				{
					{
						Addr: "1.2.3.4:8080",
						Op:   naming.Add,
					},
				},
				{
					{
						Addr: "1.2.3.4:8080",
						Op:   naming.Delete,
					},
					{
						Addr: "1.2.3.5:8080",
						Op:   naming.Add,
					},
				},
			},
		},
		{
			watchedTargetPort: targetPort{value: "8080"},
			changes: []change{
				newTestChange(watch.Added, multipleAddrSubset),
				newTestChange(watch.Modified, multipleAddrSubset),
				newTestChange(watch.Modified, modifiedMultipleAddrSubset),
				newTestChange(watch.Deleted, modifiedMultipleAddrSubset),
			},
			expectedUpdates: [][]*naming.Update{
				{
					{
						Addr: "1.2.3.3:8080",
						Op:   naming.Add,
					},
					{
						Addr: "1.2.4.4:8080",
						Op:   naming.Add,
					},
					{
						Addr: "1.2.5.5:8080",
						Op:   naming.Add,
					},
				},
				nil, // No change.
				{
					{
						Addr: "1.2.4.4:8080",
						Op:   naming.Delete,
					},
					{
						Addr: "1.2.4.5:8080",
						Op:   naming.Add,
					},
					{
						Addr: "1.2.5.5:8080",
						Op:   naming.Delete,
					},
				},
				{
					{
						Addr: "1.2.3.3:8080",
						Op:   naming.Delete,
					},
					{
						Addr: "1.2.4.5:8080",
						Op:   naming.Delete,
					},
				},
			},
		},
		{
			// Test case that did not work previously because of bug.
			watchedTargetPort: targetPort{value: "8080"},
			changes: []change{
				newTestChange(watch.Added, testAddr1),
				newTestChange(watch.Deleted, testAddr1),
				newTestChange(watch.Added, testAddr1),
			},
			expectedUpdates: [][]*naming.Update{
				{
					{
						Addr: "1.2.3.4:8080",
						Op:   naming.Add,
					},
				},
				{
					{
						Addr: "1.2.3.4:8080",
						Op:   naming.Delete,
					},
				},
				{
					{
						Addr: "1.2.3.4:8080",
						Op:   naming.Add,
					},
				},
			},
		},
		{
			watchedTargetPort: targetPort{value: "8080"},
			changes: []change{
				newTestChange(watch.Modified, testAddr1),
			},
			// It's hard to detect this case, so we just add the thing.
			expectedUpdates: [][]*naming.Update{
				{
					{
						Addr: "1.2.3.4:8080",
						Op:   naming.Add,
					},
				},
			},
		},
		// Malformed state cases. We assume this order of events will never happen:
		{
			watchedTargetPort: targetPort{value: "8080"},
			changes: []change{
				newTestChange(watch.Deleted, testAddr1),
			},
			expectedErrs: []error{errors.New("malformed internal state for addresses for target {  " +
				"{8080}}. We got delete event type with state before deletion and it does not match with that " +
				"we tracked map[]. State before deletion map[1.2.3.4:8080:{}]. Doing resync...")},
		},
		{
			// This can happen (two adds) when we do resync.
			watchedTargetPort: targetPort{value: "8080"},
			changes: []change{
				newTestChange(watch.Added, testAddr1),
				newTestChange(watch.Added, testAddr1),
			},
			expectedUpdates: [][]*naming.Update{
				{
					{
						Addr: "1.2.3.4:8080",
						Op:   naming.Add,
					},
				},
				nil,
			},
		},
	} {
		ok := t.Run("", func(t *testing.T) {
			defer leaktest.CheckTimeout(t, 10*time.Second)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			changeCh := make(chan change, 10)
			errCh := make(chan error, 1)
			w := &watcher{
				ctx:    ctx,
				target: targetEntry{port: tcase.watchedTargetPort},
				streamer: &streamer{
					changeCh: changeCh,
					errCh:    errCh,
					cancel:   func() {},
				},
				addrsState: map[string]struct{}{},

				resolvedAddrs:     resolvedAddrs.WithLabelValues(""),
				watcherErrs:       watcherErrs.WithLabelValues(""),
				watcherGotChanges: watcherGotChanges.WithLabelValues(""),
			}

			for i, change := range tcase.changes {
				changeCh <- change
				if len(tcase.expectedUpdates) > i && len(tcase.expectedUpdates[i]) == 0 {
					// if 0 updates, then next will not give us anything.
					continue
				}

				u, err := w.next(ctx)
				if len(tcase.expectedErrs) > i && tcase.expectedErrs[i] != nil {
					require.Error(t, err)
					require.Equal(t, tcase.expectedErrs[i].Error(), err.Error())
					continue
				}
				require.NoError(t, err)

				sort.Slice(u, func(i, j int) bool {
					return strings.Compare(u[i].Addr, u[j].Addr) < 0
				})
				require.Equal(t, tcase.expectedUpdates[i], u, "case %d is wrong", i)
			}
		})
		if !ok {
			return
		}
	}
}
