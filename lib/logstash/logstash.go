package logstash

import (
	"io"
	"net"
	"time"

	"github.com/mwitkow/kedge/lib/sharedflags"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

var (
	flagLogstashWriteTimeout = sharedflags.Set.Duration(
		"logstash_write_timeout", 2*time.Second,
		"Time to wait for a successfull write to logstash before dropping a log")
	flagLogstashWriteBufferSize = sharedflags.Set.Int(
		"logstash_write_buffer_size", 5000,
		"Size of the buffer for log entries for the async writer",
	)
)

// Hook sends logs to Logstash.
type Hook struct {
	hostPort  string
	conn      io.Writer
	buffer    chan *logrus.Entry
	errLogger *logrus.Logger
	formatter logrus.Formatter
}

// newHook creates a hook to be added to an instance of logger. It connects
// to Logstash on host via TCP.
func NewHook(hostPort string, formatter logrus.Formatter) (*Hook, error) {
	dial := func() (net.Conn, error) {
		conn, err := net.DialTimeout("tcp", hostPort, 2*time.Second)
		if err != nil {
			return nil, errors.Wrap(err, "Failed to establish logstash connection.")
		}

		return conn, nil
	}

	// We ignore error because we will try to reconnect inside Fire() if there is no connection.
	errLogger := logrus.New()
	hook := &Hook{
		hostPort:  hostPort,
		conn:      NewReconnectingWriter(dial, errLogger),
		buffer:    make(chan *logrus.Entry, *flagLogstashWriteBufferSize),
		errLogger: errLogger,
		formatter: formatter,
	}

	go hook.start()

	return hook, nil
}

// Fire implements the Fire method from the Hook interface. It writes to its local buffer for the Entry to be sent
// off asynchronously. If the buffer is full, it will drop the entry.
func (hook *Hook) Fire(entry *logrus.Entry) error {
	reportEntry(entry.Level)
	select {
	case hook.buffer <- entry:
	default:
		hook.errLogger.Error("Failed to write log message due to full buffer.")
		reportDropped(entry.Level, BufferFull)
	}
	return nil
}

// Levels returns all logrus levels.
func (hook Hook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (hook *Hook) start() {
	for entry := range hook.buffer {
		payload, err := hook.formatter.Format((*logrus.Entry)(entry))
		if err != nil {
			hook.errLogger.WithError(err).Errorf("Failed to write log message due to bad format: %v", err)
			reportDropped(entry.Level, BadFormat)
			continue
		}
		_, err = hook.conn.Write([]byte(payload))
		if err != nil {
			reportDropped(entry.Level, FailedToWrite)
			hook.errLogger.WithError(err).Errorf("Failed to write log message to logstash due to connection issues: %v", err)
			continue
		}
		reportRemoteSuccess(entry.Level)
	}
}
