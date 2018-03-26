package k8sresolver

import (
	"context"
	"fmt"
	"net"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/naming"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/watch"
)

// A Watcher provides name resolution updates by watching endpoints API.
// It works by watching endpoint Watch API (retries if connection broke). Returned events with
// changes inside endpoints are translated to resolution naming.Updates.
type watcher struct {
	logger logrus.FieldLogger

	ctx    context.Context
	cancel context.CancelFunc
	target targetEntry

	streamer   *streamer
	addrsState map[string]struct{}

	resolvedAddrs     prometheus.Gauge
	watcherErrs       prometheus.Counter
	watcherGotChanges prometheus.Counter
}

func startNewWatcher(
	logger logrus.FieldLogger,
	target targetEntry,
	epClient endpointClient,
	resolvedAddrs prometheus.Gauge,
	watcherErrs prometheus.Counter,
	watcherGotChanges prometheus.Counter,
) (*watcher, error) {
	// NOTE(bplotka): Would love to have proper context from above but naming.Resolver does not allow that.
	ctx, cancel := context.WithCancel(context.Background())

	s, err := startNewStreamer(ctx, target, epClient)
	if err != nil {
		return nil, err
	}

	return &watcher{
		logger:     logger,
		ctx:        ctx,
		cancel:     cancel,
		target:     target,
		streamer:   s,
		addrsState: map[string]struct{}{},

		resolvedAddrs:     resolvedAddrs,
		watcherErrs:       watcherErrs,
		watcherGotChanges: watcherGotChanges,
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
		return []*naming.Update(nil), errors.Wrap(w.ctx.Err(), "k8sresolver: watcher.Next already stopped or Next returned error already. "+
			"Note that watcher errors are not recoverable.")
	}
	u, err := w.next()
	if err != nil {
		if w.ctx.Err() == nil {
			// Only update those if watcher is not cancelled.
			w.watcherErrs.Inc()
			w.resolvedAddrs.Set(float64(0))
		}

		// Just in case.
		w.Close()
		return nil, errors.Wrap(err, "k8sresolver: ")
	}

	w.resolvedAddrs.Set(float64(len(w.addrsState)))
	return u, nil
}

// next gathers kube api endpoint watch changes and translates them to naming.Update set.
// The main complexity is fact that naming.Update can be either Add or Delete. However kube events can be Add,
// Delete or Modified. As a result we are required to maintain state. If the state is malformed we immediately return error
// which will cause resync on caller side (new resolver).
func (w *watcher) next() ([]*naming.Update, error) {
	var (
		updates         []*naming.Update
		change          change
		changeCh, errCh = w.streamer.ResultChans()
		newAddrsState   = map[string]struct{}{}
	)

	for len(updates) == 0 {
		select {
		case <-w.ctx.Done():
			// We already stopped.
			return []*naming.Update(nil), w.ctx.Err()
		case err := <-errCh:
			return []*naming.Update(nil), errors.Wrap(err, "error on reading change stream")
		case change = <-changeCh:
		}
		w.watcherGotChanges.Inc()

		for _, subset := range change.Subsets {
			var err error
			newAddrsState, err = subsetToAddresses(w.target, subset)
			if err != nil {
				return []*naming.Update(nil), errors.Wrap(err, "failed to convert k8s endpoint subset to update Addr")
			}

			if len(newAddrsState) > 0 {
				// Expected port found.
				break
			}

			// Target port not found yet. Maybe other subsets includes target one?
		}

		// We watch strictly for single service, thus we assume single "endpoints" object all the time.
		// This way we can safely treat the object for Added and Modified as new state.
		// Deleted event gives state before deletion, but we don't care. The whole object was deleted.
		switch change.typ {
		case watch.Added:
			if len(w.addrsState) > 0 {
				return []*naming.Update(nil), errors.Errorf("malformed internal state for addresses for target %v. "+
					"We got added event type, but we already have some addresses from old updates: %v. Doing resync...", w.target, w.addrsState)
			}

			for addr := range newAddrsState {
				updates = append(updates, w.addAddr(addr))
			}
		case watch.Modified:
			for addr := range newAddrsState {
				_, ok := w.addrsState[addr]
				if ok {
					// Address already exists in old state, nothing to do.
					continue
				}

				// Address does not exists in old state, let's add it.
				updates = append(updates, w.addAddr(addr))
			}

			for addr := range w.addrsState {
				_, ok := newAddrsState[addr]
				if ok {
					// Address exists in new state, nothing to do.
					continue
				}

				// Address does not exists in new state, let's remove it.
				updates = append(updates, w.delAddr(addr))
			}
		case watch.Deleted:
			if len(w.addrsState) != len(newAddrsState) {
				return []*naming.Update(nil), errors.Errorf("malformed internal state for addresses for target %v. "+
					"We got delete event type with state before deletion and it does not match with that we tracked %v. "+
					"State before deletion %v. Doing resync...", w.target, w.addrsState, newAddrsState)
			}

			for addr := range w.addrsState {
				_, ok := newAddrsState[addr]
				if !ok {
					return []*naming.Update(nil), errors.Errorf("malformed internal state for addresses for target %v. "+
						"We got delete event type with state before deletion and it does not match with that we tracked %v. "+
						"State before deletion %v. Doing resync...", w.target, w.addrsState, newAddrsState)
				}

				updates = append(updates, w.delAddr(addr))
			}
		default:
			return []*naming.Update(nil), errors.Errorf("unexpected change type %v", change.typ)
		}
	}

	return updates, nil
}

func (w *watcher) addAddr(addr string) *naming.Update {
	w.addrsState[addr] = struct{}{}
	return &naming.Update{Op: naming.Add, Addr: addr}
}

func (w *watcher) delAddr(addr string) *naming.Update {
	delete(w.addrsState, addr)
	return &naming.Update{Op: naming.Delete, Addr: addr}
}

func subsetToAddresses(t targetEntry, sub v1.EndpointSubset) (map[string]struct{}, error) {
	port, found, err := matchTargetPort(t.port, sub.Ports)
	if err != nil {
		return nil, err
	}

	addrs := map[string]struct{}{}
	if !found {
		return nil, nil
	}

	for _, address := range sub.Addresses {
		addrs[net.JoinHostPort(address.IP, port)] = struct{}{}
	}
	return addrs, nil
}

// matchTargetPort searches for specified port in targetPort and returns port number as string. Basically:
//
// service.namespace - means no target port specified.
// service.namespace:123 - means not named port, so we should just use, but only if it's present in endpoint.
// service.namespace:abc - means named port.
func matchTargetPort(targetPort targetPort, ports []v1.EndpointPort) (string, bool, error) {
	if len(ports) == 0 {
		return "", false, errors.Errorf("retrieved subset update contains no port")
	}

	if targetPort == noTargetPort {
		if len(ports) > 1 {
			return "", false, errors.Errorf("we got %v ports and target port is not specified. Don't know what to choose", ports)
		}
		return fmt.Sprintf("%v", ports[0].Port), true, nil
	}

	for _, p := range ports {
		if targetPort.isNamed && p.Name == targetPort.value {
			return fmt.Sprintf("%v", p.Port), true, nil
		}

		// Even that we have target specified as number value we want to ensure we have endpoint for it.
		if !targetPort.isNamed && fmt.Sprintf("%v", p.Port) == targetPort.value {
			return targetPort.value, true, nil
		}
	}

	// No port found.
	return "", false, nil
}
