package k8sresolver

import (
	"context"
	"net"
	"strconv"
	"time"

	"github.com/jpillora/backoff"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/naming"
)

var (
	watchRetryBackoff = &backoff.Backoff{
		Min: 20 * time.Millisecond,
		Max: 3 * time.Second,
	}
)

type watchResult struct {
	ep  *event
	err error
}

// A Watcher provides name resolution updates by watching endpoints API.
// It works by watching endpoint Watch API (retries if connection broke). Returned events with
// changes inside endpoints are translated to resolution naming.Updates.
type watcher struct {
	ctx    context.Context
	cancel context.CancelFunc

	target      targetEntry
	watchChange chan watchResult
	lastUpdates map[string]struct{}
}

func startNewWatcher(logger logrus.FieldLogger, target targetEntry, epClient endpointClient) *watcher {
	// NOTE(bplotka): Would love to have proper context from above but naming.Resolver does not allow that.
	ctx, cancel := context.WithCancel(context.Background())
	w := &watcher{
		ctx:         ctx,
		cancel:      cancel,
		target:      target,
		watchChange: make(chan watchResult),
		lastUpdates: make(map[string]struct{}),
	}

	go startWatchingEndpointsChanges(ctx, logger, target, epClient, w.watchChange, watchRetryBackoff, 0)
	return w
}

// Close closes the watcher, cleaning up any open connections.
func (w *watcher) Close() {
	w.cancel()
}

// Next updates the endpoints for the targetEntry being watched.
func (w *watcher) Next() ([]*naming.Update, error) {
	if w.ctx.Err() != nil {
		// We already stopped.
		return []*naming.Update(nil), w.ctx.Err()
	}

	updates := make([]*naming.Update, 0)
	updatedEndpoints := make(map[string]struct{})
	var event event
	select {
	case <-w.ctx.Done():
		// We already stopped.
		return []*naming.Update(nil), w.ctx.Err()
	case r := <-w.watchChange:
		if r.err != nil {
			return []*naming.Update(nil), errors.Wrap(r.err, "error on reading event stream")
		}
		event = *r.ep
	}

	// Translate kube api endpoint watch event to resolver address and put into map for easier lookup.
	for _, subset := range event.Object.Subsets {
		updatedAddresses, err := subsetToAddresses(w.target, subset)
		if err != nil {
			return []*naming.Update(nil), errors.Wrap(err, "failed to convert k8s endpoint subset to update Addr")
		}

		for _, address := range updatedAddresses {
			updatedEndpoints[address] = struct{}{}
		}
	}

	// Create updates to add new endpoints.
	for addr, md := range updatedEndpoints {
		if _, ok := w.lastUpdates[addr]; ok {
			continue
		}

		updates = append(updates, &naming.Update{Op: naming.Add, Addr: addr, Metadata: md})
	}
	// Create updates to delete old endpoints.
	for addr := range w.lastUpdates {
		if _, ok := updatedEndpoints[addr]; ok {
			continue
		}
		updates = append(updates, &naming.Update{Op: naming.Delete, Addr: addr, Metadata: nil})
	}

	w.lastUpdates = updatedEndpoints
	return updates, nil
}

type endpoints struct {
	Kind       string   `json:"kind"`
	APIVersion string   `json:"apiVersion"`
	Metadata   metadata `json:"metadata"`
	Subsets    []subset `json:"subsets"`
}

type metadata struct {
	Name            string `json:"name"`
	ResourceVersion string `json:"resourceVersion"`
}

type subset struct {
	Addresses []address `json:"addresses"`
	Ports     []port    `json:"ports"`
}

type address struct {
	IP string `json:"ip"`
}

type port struct {
	Name string `json:"name"`
	Port int    `json:"value"`
}

func subsetToAddresses(t targetEntry, sub subset) ([]string, error) {
	if len(sub.Ports) == 0 {
		return []string(nil), errors.Errorf("Retrieved subset update contains no port")
	}

	var port string
	if t.port == noTargetPort {
		// Get first one spotted.
		port = strconv.Itoa(sub.Ports[0].Port)
	} else if t.port.isNamed {
		for _, p := range sub.Ports {
			if p.Name == t.port.value {
				port = strconv.Itoa(p.Port)
				break
			}
		}
	} else {
		port = t.port.value
	}

	var updatedAddresses []string
	for _, address := range sub.Addresses {
		updatedAddresses = append(updatedAddresses, net.JoinHostPort(address.IP, port))
	}

	return updatedAddresses, nil
}
