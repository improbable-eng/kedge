// Copyright 2017 Michal Witkowski. All Rights Reserved.
// See LICENSE for licensing terms.

package http_logrus

import (
	"time"

	"net/http"

	"github.com/Sirupsen/logrus"
	"github.com/pressly/chi/middleware"
	"golang.org/x/net/context"
)

// TODO(mwitkow): Cargo-cult tests from https://github.com/mwitkow/go-grpc-middleware/tree/master/logging

var (
	// SystemField is used in every log statement made through http_logrus. Can be overwritten before any initialization code.
	SystemField = "http"
)

func Middleware(entry *logrus.Entry, opts ...Option) func(http.Handler) http.Handler {
	return func(nextHandler http.Handler) http.Handler {
		o := evaluateOptions(opts)
		return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
			wrappedResp := wrapResponse(req, resp)
			nCtx := newLoggerForCall(req.Context(), entry, req)
			startTime := time.Now()
			nextHandler.ServeHTTP(wrappedResp, req.WithContext(nCtx))
			postCallFields := logrus.Fields{
				"http.status":          wrappedResp.BytesWritten(),
				"http.serving.time_ms": time.Now().Sub(startTime),
				"http.time_ns":         time.Now().Sub(startTime),
			}
			level := o.levelFunc(wrappedResp.Status())
			levelLogf(
				Extract(nCtx).WithFields(postCallFields), // re-extract logger from newCtx, as it may have extra fields that changed in the holder.
				level,
				"handled")
		})
	}
}

func newLoggerForCall(ctx context.Context, entry *logrus.Entry, r *http.Request) context.Context {
	callLog := entry.WithFields(
		logrus.Fields{
			"system":           SystemField,
			"http.url.path":    r.URL.Path,
			"http.host":        r.URL.Host,
			"http.remote":      r.RemoteAddr,
			"http.proto_major": r.ProtoMajor,
		})
	return toContext(ctx, callLog)
}

func wrapResponse(req *http.Request, resp http.ResponseWriter) middleware.WrapResponseWriter {
	if wrapped, ok := resp.(middleware.WrapResponseWriter); ok {
		return wrapped
	} else {
		return middleware.NewWrapResponseWriter(resp, req.ProtoMajor)
	}
}

func levelLogf(entry *logrus.Entry, level logrus.Level, format string, args ...interface{}) {
	switch level {
	case logrus.DebugLevel:
		entry.Debugf(format, args...)
	case logrus.InfoLevel:
		entry.Infof(format, args...)
	case logrus.WarnLevel:
		entry.Warningf(format, args...)
	case logrus.ErrorLevel:
		entry.Errorf(format, args...)
	case logrus.FatalLevel:
		entry.Fatalf(format, args...)
	case logrus.PanicLevel:
		entry.Panicf(format, args...)
	}
}
