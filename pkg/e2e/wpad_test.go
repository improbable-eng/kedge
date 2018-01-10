package e2e

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"testing"
	"time"

	"github.com/improbable-eng/thanos/pkg/runutil"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const expectedPACFile = `function FindProxyForURL(url, host) {
	var proxy = "PROXY 127.0.0.1:18070; DIRECT";
	var direct = "DIRECT";

	// no proxy for local hosts without domain:
	if(isPlainHostName(host)) return direct;

	// We only proxy http, not even https.
	if (
		url.substring(0, 4) == "ftp:" ||
		url.substring(0, 6) == "rsync:" ||
		url.substring(0, 6) == "https:"
	)
		return direct;

	// Commented for debug purposes.
	// Use direct connection whenever we have direct network connectivity.
	//if (isResolvable(host)) {
	//    return direct
	//}
	if (shExpMatch(host, "*.*.internal.example.com")) {
		return proxy;
	}

	return direct;
}`

func TestWinchPAC(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	exit, err := spinup(t, ctx, config{winch: true})
	if err != nil {
		t.Error(err)
		cancel()
		return
	}

	defer func() {
		cancel()
		<-exit
	}()

	err = runutil.Retry(time.Second, ctx.Done(), func() error {
		if err = assertRunning(exit); err != nil {
			t.Error(err)
			return nil
		}

		res, err := queryWPAD(ctx)
		if err != nil {
			return err
		}

		assert.Equal(t, expectedPACFile, res)

		return nil
	})
	require.NoError(t, err)
}

func assertRunning(unexpectedExit chan error) error {
	select {
	case err := <-unexpectedExit:
		return errors.Wrap(err, "Some process exited unexpectedly")
	default:
		return nil
	}
}

func queryWPAD(ctx context.Context) (string, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%s/wpad.dat", httpWinchPort), nil)
	if err != nil {
		return "", err
	}

	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		return "", err
	}

	defer resp.Body.Close()

	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(b), nil
}
