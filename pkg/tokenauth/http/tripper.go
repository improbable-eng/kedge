package httpauth

import (
	"fmt"
	"net/http"

	"github.com/improbable-eng/kedge/pkg/tokenauth"
	"github.com/pkg/errors"
)

type Tripper struct {
	parent     http.RoundTripper
	auth       tokenauth.Source
	headerName string
}

// NewTripper constructs Tripper that is able to inject any token as Bearer inside given Header name.
func NewTripper(parent http.RoundTripper, auth tokenauth.Source, headerName string) http.RoundTripper {
	return &Tripper{
		parent:     parent,
		auth:       auth,
		headerName: headerName,
	}
}

// RoundTrip wraps parent RoundTrip and injects retrieved Token into specified Header.
func (t *Tripper) RoundTrip(req *http.Request) (*http.Response, error) {
	token, err := t.auth.Token(req.Context())
	if err != nil {
		return nil, errors.Wrap(err, "httpauth.Tripper: failed to retrieve valid Auth Token")
	}

	req.Header.Set(t.headerName, fmt.Sprintf("Bearer %s", token))
	return t.parent.RoundTrip(req)
}
