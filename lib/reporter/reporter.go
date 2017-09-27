package reporter

import (
	"context"
	"net/http"
	"runtime"
	"sync"

	"github.com/mwitkow/go-httpwares"
	"github.com/mwitkow/go-httpwares/tags"
	"github.com/mwitkow/kedge/lib/http/header"
	"github.com/mwitkow/kedge/lib/metrics"
	"github.com/mwitkow/kedge/lib/reporter/errtypes"
	"github.com/sirupsen/logrus"
)

var (
	ctxKey = struct{}{}
)

// Tracker is used to report an error spotted by kedge itself (error that happen when request was not succesfully proxied because of some reason).
// It covers only first seen error and warns when ReportError is called twice on the same request.
//
// For Kedge flow there will be only one tracker living for each request shared by both handler and reverse proxy tripperwares.
// This is thanks of reverse proxy preserving request's context.
type Tracker struct {
	mu sync.Mutex

	err     error
	errType errtypes.Type
}

// ReportError reports kedge proxy error.
func (t *Tracker) ReportError(errType errtypes.Type, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.err != nil {
		logrus.Warn("Reporting %q error ignored, hidden by existing %q", string(errType), string(t.errType))
		return
	}

	t.err = err
	t.errType = errType
}

func (t *Tracker) ErrType() errtypes.Type {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.err == nil {
		return errtypes.OK
	}
	return t.errType
}

func (t *Tracker) Error() (errtypes.Type, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.err == nil {
		return errtypes.OK, nil
	}
	return t.errType, t.err
}

// ExtractInbound returns existing tracker or does lazy creation of new one to be used.
// NOTE that still reqWrappedWithTracker function needs to be invoked to save this tracker into request's context.
// This is due to cost of copying context and request around to just add a value to context.
func Extract(req *http.Request) *Tracker {
	t, ok := extractFromCtx(req.Context())
	if !ok {
		// Should we panic here?
		_, file, no, _ := runtime.Caller(1)
		logrus.Errorf("reporter.Extract was invoked without reporter.Middleware. Reporting will not work. Called in: %s#%d", file, no)
		t = &Tracker{}
	}
	return t
}

// extractFromCtx returns a pre-existing *Tracker object from the given context.
func extractFromCtx(ctx context.Context) (*Tracker, bool) {
	t, ok := ctx.Value(ctxKey).(*Tracker)
	return t, ok
}

// ReqWrappedWithTracker returns copy of HTTP request with tracker in context.
func ReqWrappedWithTracker(req *http.Request, t *Tracker) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), ctxKey, t))
}

// Middleware reports last Kedge proxy error on each HTTP request (if spotted) by incrementing metrics and producing
// log line.
func Middleware(logger logrus.FieldLogger) httpwares.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
			if _, ok := extractFromCtx(req.Context()); ok {
				logger.Error("reporter.Middleware was called second time in the same channel.")

				next.ServeHTTP(resp, req)
				return
			}

			t := &Tracker{}
			reqWithTracker := ReqWrappedWithTracker(req, t)
			next.ServeHTTP(resp, reqWithTracker)

			incMetricOnFinish(reqWithTracker)
			logOnFinish(logger, reqWithTracker)
		})
	}
}

// incMetricOnFinish increments metric with backend_name and err type labels in ERROR case only.
func incMetricOnFinish(inboundReq *http.Request) {
	t := Extract(inboundReq)
	errType := t.ErrType()
	if errType == errtypes.OK {
		// No metric to report.
		return
	}

	tags := http_ctxtags.ExtractInbound(inboundReq).Values()

	backendName, _ := tags[http_ctxtags.TagForHandlerName].(string)
	metrics.KedgeProxyErrors.WithLabelValues(backendName, string(errType)).Inc()
}

// logOnFinish prints a log line in ERROR case only, in DEBUG level.
// This is because most of these errors are usually just user errors.
// The only exception is when RequestKedgeForceInfoLogs is requested using request header. In that case:
// - OK log is printed as INFO
// - ERROR log is printed as ERROR.
func logOnFinish(logger logrus.FieldLogger, inboundReq *http.Request) {
	t := Extract(inboundReq)
	tags := http_ctxtags.ExtractInbound(inboundReq).Values()
	forceLoggingHeaderVal := inboundReq.Header.Get(header.RequestKedgeForceInfoLogs)
	errType, err := t.Error()
	if err == nil {
		// Nothing to report, but caller might want to have still the OK log line.
		if forceLoggingHeaderVal != "" {
			logger.WithFields(tags).
				WithField("force-info-log-header", forceLoggingHeaderVal).
				Info("Request proxied inside cluster")
		}
		return
	}

	if forceLoggingHeaderVal == "" {
		logger.WithFields(tags).WithError(err).
			Debugf("Failed to proxy request inside cluster. %v", errType)
		return
	}

	// Caller requested these errors in INFO log request. Since it is error, we will log it as ERROR.
	logger.WithFields(tags).WithError(err).
		WithField("force-info-log-header", forceLoggingHeaderVal).
		Errorf("Failed to proxy request inside cluster. %v", errType)
}

// SetKedgeErrorHeaders adds Kedge Error headers useful to immediately see kedge error on HTTP response.
// NOTE: This method can be invoked only before resp.WriteHeader(...)
func SetKedgeErrorHeaders(headerMap http.Header, t *Tracker) {
	errType, err := t.Error()
	if err == nil {
		return
	}
	headerMap.Set(header.ResponseKedgeError, err.Error())
	headerMap.Set(header.ResponseKedgeErrorType, string(errType))
}

// SetWinchErrorHeaders adds winch Error headers useful to immediately see winch error on HTTP response.
// NOTE: This method can be invoked only before resp.WriteHeader(...)
func SetWinchErrorHeaders(headerMap http.Header, t *Tracker) {
	errType, err := t.Error()
	if err == nil {
		return
	}
	headerMap.Set(header.ResponseKedgeError, err.Error())
	headerMap.Set(header.ResponseKedgeErrorType, string(errType))
}

// TODO: Interceptor for GRPC
