package e2e

import "time"

// Retry executes f every interval seconds until timeout or no error is returned from f.
// This function is copy-pasted from "github.com/improbable-eng/thanos/pkg/runutil"
func Retry(interval time.Duration, stopc <-chan struct{}, f func() error) error {
	tick := time.NewTicker(interval)
	defer tick.Stop()

	var err error
	for {
		if err = f(); err == nil {
			return nil
		}
		select {
		case <-stopc:
			return err
		case <-tick.C:
		}
	}
}
