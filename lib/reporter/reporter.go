package reporter

import (
	"net/http"
	"github.com/mwitkow/go-httpwares"
	"context"
	"fmt"
	"github.com/mwitkow/kedge/lib/reporter/errtypes"
	"github.com/mwitkow/kedge/lib/metrics"
	"github.com/mwitkow/go-httpwares/tags"
	"github.com/sirupsen/logrus"
)

var (
	ctxKey = struct{}{}
)

type ReportableError struct {
	errType errtypes.Type
	err error
}

func (e ReportableError) Type() errtypes.Type {
	return e.errType
}

func (e ReportableError) Error() string {
	return fmt.Sprintf("Type: %s Err: %v", string(e.errType), e.err)
}

func WrapError(errType errtypes.Type, err error) error {
	return ReportableError{
		errType: errType,
		err: err,
	}
}

// ProxyTracker is an object living for the whole proxy state: from getting inbound request to redirecting to the proper backend.
type ProxyTracker interface {
	ReportError(errtypes.Type, error)
}

// ExtractInbound returns a pre-existing Tags object in the request's Context meant for server-side.
// If the context wasn't set in the Middleware, a no-op Tag storage is returned that will *not* be propagated in context.
func Extract(req *http.Request) ProxyTracker {
	t, ok := ExtractFromCtx(req.Context())
	if !ok {
		return &noopTracker{}
	}
	return t
}

// ExtractFromCtx returns a pre-existing Tags object in the request's Context.
// If the context wasn't set in a tag interceptor, a no-op Tag storage is returned that will *not* be propagated in context.
func ExtractFromCtx(ctx context.Context) (*tracker, bool) {
	t, ok := ctx.Value(ctxKey).(*tracker)
	return t, ok
}

func ReverseProxyDirector(req *http.Request) {
	setTrackerToRequest(req, Extract(req))
}

func setTrackerToRequest(req *http.Request, t ProxyTracker) {
	if _, ok := t.(*tracker); !ok {
		return
	}

	req = req.WithContext(context.WithValue(req.Context(), ctxKey, t))
}

func Middleware(logger logrus.FieldLogger) httpwares.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
			if _, ok := ExtractFromCtx(req.Context()); ok {
				next.ServeHTTP(resp, req)
				return
			}

			t := &tracker{}
			setTrackerToRequest(req, t)
			next.ServeHTTP(resp, req)

			tags := http_ctxtags.ExtractInbound(req).Values()

			// Record metrics with backend_name and err type labels.
			metrics.KedgeProxyErrors.WithLabelValues(
				tags[http_ctxtags.TagForHandlerName].(string),
				string(t.lastSeenErr.errType)).Inc()
			// Log error.
			logger.WithFields(tags).WithError(t.lastSeenErr).Error("Failed to proxy request inside cluster.")

		})
	}
}

func Tripperware() httpwares.Tripperware {
	return func(next http.RoundTripper) http.RoundTripper {
		return httpwares.RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			t, ok := ExtractFromCtx(req.Context())
			if !ok {
				// No reporting.
				return next.RoundTrip(req)
			}

			resp, err := next.RoundTrip(req)
			// Is that error reportable?
			if err, ok := err.(ReportableError); ok {
				t.ReportError(err.Type(), err.err)
			}
			return resp, err
		})
	}
}

// TODO: Interceptor for GRPC

type tracker struct {
	lastSeenErr ReportableError
}

func (t *tracker) ReportError(errType errtypes.Type, err error) {
	t.lastSeenErr = ReportableError{
		err : err,
		errType: errType,
	}
}

type noopTracker struct {}

func (*noopTracker) ReportError(errtypes.Type, error) {}