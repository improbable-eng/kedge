package e2e

import (
	"context"
	"testing"
	"time"

	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"

	"github.com/improbable-eng/thanos/pkg/runutil"
	"github.com/stretchr/testify/require"
	"errors"
)

func TestHTTPEndpointCall(t *testing.T) {
	const name = "kedge"

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	unexpectedExit, err := spinup(t, ctx, config{winch: true, kedge: true, testEndpoint: true})
	require.NoError(t, err)

	err = runutil.Retry(time.Second, ctx.Done(), func() error {
		if err = assertRunning(unexpectedExit); err != nil {
			t.Error(err)
			return nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10 *time.Second)
		defer cancel()

		status, body, err := httpHelloViaWinchAndKedge(ctx, name)
		if err != nil {
			return err
		}

		if status != http.StatusOK {
			if status == http.StatusBadGateway {
				return errors.New("not ready")
			}

			t.Errorf("Unexpected status code: %s. Resp: %v", status, body)
			return nil
		}

		if body != expectedResponse(name) {
			t.Errorf("Unexpected response: %s; Exp: %s", body, expectedResponse(name))
			return nil
		}

		return nil
	})
	require.NoError(t, err)
}

const endpointDNS = "test_endpoint.localhost.internal.example.com"

func httpHelloViaWinchAndKedge(ctx context.Context, name string) (int, string, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("http://%s:%s/?name=%s", endpointDNS, httpTestEndpointPort, name), nil)
	if err != nil {
		return 0, "", err
	}

	defTransport := &http.Transport{
		Proxy: func(*http.Request) (*url.URL, error) {
			// Call via winch.
			return url.Parse(fmt.Sprintf("http://127.0.0.1:%s", httpWinchPort))
		},
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
			DualStack: true,
		}).DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	resp, err := (&http.Client{Transport: defTransport}).Do(req.WithContext(ctx))
	if err != nil {
		return 0, "", err
	}

	defer resp.Body.Close()

	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return 0, "", err
	}

	return resp.StatusCode, string(b), nil
}
