package k8sresolver

import (
	"context"
	"fmt"
	"net"

	"github.com/pkg/errors"
	"google.golang.org/grpc/naming"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/watch"
)

// A Watcher provides name resolution updates by watching endpoints API.
// It works by watching endpoint Watch API (retries if connection broke). Returned events with
// changes inside endpoints are translated to resolution naming.Updates.
type watcher struct {
	ctx    context.Context
	cancel context.CancelFunc
	target targetEntry

	streamer  *streamer
	endpoints map[key]string
}

func startNewWatcher(target targetEntry, epClient endpointClient) (*watcher, error) {
	// NOTE(bplotka): Would love to have proper context from above but naming.Resolver does not allow that.
	ctx, cancel := context.WithCancel(context.Background())

	s, err := startNewStreamer(ctx, target, epClient)
	if err != nil {
		return nil, err
	}

	return &watcher{
		ctx:       ctx,
		cancel:    cancel,
		target:    target,
		streamer:  s,
		endpoints: map[key]string{},
	}, nil
}

// Close closes the watcher, cleaning up any open connections.
func (w *watcher) Close() {
	w.cancel()
}

// Next updates the endpoints for the targetEntry being watched.
// As from Watcher interface: It should return an error if and only if Watcher cannot recover.
func (w *watcher) Next() ([]*naming.Update, error) {
	if w.ctx.Err() != nil {
		// We already stopped.
		return []*naming.Update(nil), errors.Wrap(w.ctx.Err(), "watcher.Next already stopped or Next returned error already. "+
			"Note that watcher errors are not recoverable.")
	}
	u, err := w.next()
	if err != nil {
		// Just in case.
		w.Close()
		return u, errors.Wrap(err, "k8sresolver")
	}
	return u, nil
}

type key struct {
	// EndpointAddress fields (usually pod).
	ns, name string
}

func keyFromAddr(address v1.EndpointAddress) (key, error) {
	if address.TargetRef == nil {
		return key{}, errors.New("address targetRef is empty. Cannot maintain internal state.")
	}
	return key{ns: address.TargetRef.Namespace, name: address.TargetRef.Name}, nil
}

func (w *watcher) next() ([]*naming.Update, error) {
	var (
		updates           []*naming.Update
		change            change
		changeCh, errCh   = w.streamer.ResultChans()
		endpointsToUpdate = map[key]string{}
	)

	select {
	case <-w.ctx.Done():
		// We already stopped.
		return []*naming.Update(nil), w.ctx.Err()
	case err := <-errCh:
		return []*naming.Update(nil), errors.Wrap(err, "error on reading change stream")
	case change = <-changeCh:
	}

	// Translate kube api endpoint watch change to resolver address and put into map for easier lookup.
	for _, subset := range change.Subsets {
		var err error
		endpointsToUpdate, err = subsetToAddresses(w.target, subset)
		if err != nil {
			return []*naming.Update(nil), errors.Wrap(err, "failed to convert k8s endpoint subset to update Addr")
		}

		if len(endpointsToUpdate) > 0 {
			// Expected port found.
			break
		}

		// Target port not found yet. Maybe other subsets includes target one?
	}

	switch change.typ {
	case watch.Added:
		// Create updates to add new endpoints.
		for k, addr := range endpointsToUpdate {
			_, ok := w.endpoints[k]
			if ok {
				return []*naming.Update(nil), errors.Errorf("malformed internal state for endpoints. "+
					"On added event type, we got update for %v that already exists in %v. Doing resync...", k, w.endpoints)
			}

			w.endpoints[k] = addr
			updates = append(updates, &naming.Update{Op: naming.Add, Addr: addr})
		}
	case watch.Modified:
		for k, addr := range endpointsToUpdate {
			oldAddr, ok := w.endpoints[k]
			if !ok {
				return []*naming.Update(nil), errors.Errorf("malformed internal state for endpoints. "+
					"On modified event type, we got update for %v that does not exists in %v. Doing resync...", k, w.endpoints)
			}

			updates = append(updates, &naming.Update{Op: naming.Delete, Addr: oldAddr})
			w.endpoints[k] = addr
			updates = append(updates, &naming.Update{Op: naming.Add, Addr: addr})
		}
	case watch.Deleted:
		// Create updates to delete old endpoints.
		for k, addr := range endpointsToUpdate {
			_, ok := w.endpoints[k]
			if !ok {
				return []*naming.Update(nil), errors.Errorf("malformed internal state for endpoints. "+
					"On delete event type, we got update for %v that does not exists in %v. Doing resync...", k, w.endpoints)
			}

			updates = append(updates, &naming.Update{Op: naming.Delete, Addr: addr})
			delete(w.endpoints, k)
		}
	}
	return updates, nil
}

func subsetToAddresses(t targetEntry, sub v1.EndpointSubset) (map[key]string, error) {
	if len(sub.Ports) == 0 {
		return nil, errors.Errorf("retrieved subset update contains no port")
	}
	var port string

	// targetEntry that is specified for this watcher controls what port we should use.
	if t.port == noTargetPort {
		// Get first one spotted.
		port = fmt.Sprintf("%v", sub.Ports[0].Port)
	} else if t.port.isNamed {
		for _, p := range sub.Ports {
			if p.Name == t.port.value {
				port = fmt.Sprintf("%v", p.Port)
				break
			}
		}
		if port == "" {
			// Not found.
			return map[key]string{}, nil
		}
	} else {
		port = t.port.value
	}

	addrs := map[key]string{}
	for _, address := range sub.Addresses {
		k, err := keyFromAddr(address)
		if err != nil {
			return nil, err
		}
		addrs[k] = net.JoinHostPort(address.IP, port)
	}
	return addrs, nil
}
