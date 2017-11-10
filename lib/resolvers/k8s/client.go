package k8sresolver

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/improbable-eng/kedge/lib/k8s"
	"github.com/pkg/errors"
)

type endpointClient interface {
	StartChangeStream(ctx context.Context, t targetEntry) (io.ReadCloser, error)
}

type client struct {
	k8sClient *k8s.APIClient
}

// StartChangeStream starts stream of changes from watch endpoint.
// See https://kubernetes.io/docs/api-reference/v1.7/#watch-132
// NOTE: In the beginning of stream, k8s will give us sufficient info about current state. (No need to GET first)
func (c *client) StartChangeStream(ctx context.Context, t targetEntry) (io.ReadCloser, error) {
	epWatchURL := fmt.Sprintf("%s/api/v1/watch/namespaces/%s/endpoints/%s",
		c.k8sClient.Address,
		t.namespace,
		t.service,
	)

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
