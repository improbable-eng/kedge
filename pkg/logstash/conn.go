package logstash

import (
	"net"
	"time"

	"github.com/jpillora/backoff"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type dialFunc func() (net.Conn, error)

// ReconnectingWriter wraps a net.Conn such that it will never fail to write and will attempted to reestablish
// the connection whenever an error occurs.
type ReconnectingWriter struct {
	underlyingConn net.Conn
	dial           dialFunc
	backoff        *backoff.Backoff
	errLogger      logrus.FieldLogger
}

func NewReconnectingWriter(dial dialFunc, errLogger logrus.FieldLogger) *ReconnectingWriter {
	return &ReconnectingWriter{
		dial:      dial,
		errLogger: errLogger,
		backoff: &backoff.Backoff{
			Factor: 2,
			Min:    50 * time.Millisecond,
			Max:    2 * time.Second,
		},
	}
}

// Write will run the following retry all of the following in a loop until it manages one successful write:
// * attempt to re-establish a connection if a connection is not available
// * attempt to write the given bytes if a connection is available and exit if it succeeds
// * reset the underlying connection if write fails
//
// Deadline is set for all writes as specified in the flag.
func (c *ReconnectingWriter) Write(b []byte) (int, error) {
	for {
		n, err := c.send(b)
		if err != nil {
			c.errLogger.WithError(err).Warnf("failed to write")
			time.Sleep(c.backoff.Duration())
			continue
		}
		return n, nil
	}
}

func (c *ReconnectingWriter) send(b []byte) (int, error) {
	if c.underlyingConn == nil {
		conn, err := c.dial()
		if err != nil {
			return 0, errors.Wrap(err, "attempted to reestablish connection but failed")
		}
		c.underlyingConn = conn
	}

	err := c.underlyingConn.SetWriteDeadline(time.Now().Add(*flagLogstashWriteTimeout))
	if err != nil {
		c.resetConn()
		return 0, errors.Wrap(err, "could not set deadline for write")
	}
	n, err := c.underlyingConn.Write(b)
	if err != nil {
		c.resetConn()
		return 0, errors.Wrap(err, "could not write to external connection")
	}
	return n, nil
}

func (c *ReconnectingWriter) resetConn() {
	c.underlyingConn.Close()
	c.underlyingConn = nil
	c.backoff.Reset()
}
