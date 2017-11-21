package tripperware

import (
	"fmt"
	"net/http"

	"os"

	"github.com/google/uuid"
	"github.com/improbable-eng/go-httpwares/tags"
	"github.com/improbable-eng/kedge/lib/http/ctxtags"
	"github.com/improbable-eng/kedge/lib/http/header"
)

// setHeaderTripperware is a piece of tripperware that sets specified header value into request.
// Optionally you can specify tag name that to put the header value into.
type setHeaderTripperware struct {
	headerName    string
	headerValueFn func() string
	tagName       string

	parent http.RoundTripper
}

func (t *setHeaderTripperware) RoundTrip(req *http.Request) (*http.Response, error) {
	value := t.headerValueFn()
	req.Header.Set(t.headerName, value)

	if t.tagName != "" {
		tags := http_ctxtags.ExtractInbound(req)
		tags.Set(t.tagName, value)
	}
	return t.parent.RoundTrip(req)
}

// WrapForRequestID wraps tripperware with new one that appends unique request ID to "X-Kedge-Request-ID" header allowing
// better debug tracking.
func WrapForRequestID(prefix string, parentTransport http.RoundTripper) http.RoundTripper {
	return &setHeaderTripperware{
		headerName: header.RequestKedgeRequestID,
		headerValueFn: func() string {
			return fmt.Sprintf("%s%s", prefix, uuid.New().String())
		},
		tagName: ctxtags.TagRequestID,
		parent:  parentTransport,
	}
}

// WrapForDebug wraps tripperware with new one that signals kedge to use INFO logs for all DEBUG logs.
func WrapForDebug(parentTransport http.RoundTripper) http.RoundTripper {
	return &setHeaderTripperware{
		headerName: header.RequestKedgeForceInfoLogs,
		headerValueFn: func() string {
			return os.ExpandEnv("winch-$USER")
		},
		parent: parentTransport,
	}
}
