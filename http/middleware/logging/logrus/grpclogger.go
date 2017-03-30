// Copyright 2017 Michal Witkowski. All Rights Reserved.
// See LICENSE for licensing terms.

package http_logrus

import (
	"log"

	"github.com/Sirupsen/logrus"
)

// AsHttpLogger returns the given logrus instance as an HTTP logger.
func AsHttpLogger(logger *logrus.Entry) *log.Logger {
	return log.New(&loggerWriter{l: logger.WithField("system", SystemField)}, "", 0)
}

// loggerWriter is needed to use a Writer so that you can get a std log.Logger.
type loggerWriter struct {
	l *logrus.Entry
}

func (w *loggerWriter) Write(p []byte) (n int, err error) {
	w.l.Warn(string(p))
	return len(p), nil
}
