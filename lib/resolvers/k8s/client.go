package k8sresolver

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/pkg/errors"
)

type endpointClient interface {
	StartChangeStream(ctx context.Context, t targetEntry, resourceVersion int) (io.ReadCloser, error)
}

type client struct {
	k8sURL    string
	k8sClient *http.Client
}

// StartChangeStream starts stream of changes from watch endpoint.
// See https://kubernetes.io/docs/api-reference/v1.7/#watch-132
// NOTE: In the beginning of stream, k8s will give us sufficient info about current state. (No need to GET first)
// `resourceVersion` is just a revision.
func (c *client) StartChangeStream(ctx context.Context, t targetEntry, resourceVersion int) (io.ReadCloser, error) {
	epWatchURL := fmt.Sprintf("%s/api/v1/watch/namespaces/%s/endpoints/%s",
		c.k8sURL,
		t.namespace,
		t.service,
	)

	if resourceVersion != 0 {
		epWatchURL = fmt.Sprintf("%s?resourceVersion=%d", epWatchURL, resourceVersion)
	}

	return c.startGET(ctx, epWatchURL)
}

// NOTE: It is caller responsibility to read body through and close it.
func (c *client) startGET(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to create new GET request %s", url)
	}

	resp, err := c.k8sClient.Do(req.WithContext(ctx))
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to do GET %s request", url)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, errors.Errorf("Invalid response code %d on GET %s request", resp.StatusCode, url)
	}

	return resp.Body, nil
}
