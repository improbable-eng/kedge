// Copyright 2017 Michal Witkowski. All Rights Reserved.
// See LICENSE for licensing terms.

package http_logrus

import (
	"github.com/Sirupsen/logrus"
	"github.com/mwitkow/go-grpc-middleware/logging"
)

var (
	defaultOptions = &options{
		levelFunc: DefaultCodeToLevel,
	}
)

type options struct {
	levelFunc          CodeToLevel
	fieldExtractorFunc grpc_logging.RequestLogFieldExtractorFunc
}

func evaluateOptions(opts []Option) *options {
	optCopy := &options{}
	*optCopy = *defaultOptions
	for _, o := range opts {
		o(optCopy)
	}
	return optCopy
}

type Option func(*options)

// CodeToLevel function defines the mapping between gRPC return codes and interceptor log level.
type CodeToLevel func(httpStatusCode int) logrus.Level

// WithLevels customizes the function for mapping gRPC return codes and interceptor log level statements.
func WithLevels(f CodeToLevel) Option {
	return func(o *options) {
		o.levelFunc = f
	}
}

// DefaultCodeToLevel is the default implementation of gRPC return codes and interceptor log level.
func DefaultCodeToLevel(httpStatusCode int) logrus.Level {
	if httpStatusCode < 400 {
		return logrus.InfoLevel
	} else if httpStatusCode < 500 {
		return logrus.WarnLevel
	} else if httpStatusCode < 600 {
		return logrus.ErrorLevel
	} else {
		return logrus.ErrorLevel
	}
}
