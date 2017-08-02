package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/mwitkow/kedge/lib/sharedflags"
)

const (
	errorKey      = "error"
	errorStackKey = "stack"

	// defaultTimestampFormat is the time format with milliseconds.
	defaultTimestampFormat string = "2006-01-02T15:04:05.999Z07:00"

	// maxMessageBodySize is the max length of the log message before it is trimmed.
	maxMessageBodySize int = 1 << 14
)


var (
	TimestampFormat string = defaultTimestampFormat
	flagLogstashLogTags = sharedflags.Set.StringSlice("logstash_log_tags", []string{"kedge"},
		"@tags field to inject to while formatting log message for logstash.")

)

func newLogstashFormatter() (*logstashFormatter, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	return &logstashFormatter{
		Hostname: hostname,
	}, nil
}

// logstashFormatter generates json in logstash format.
// See the Logstash website http://logstash.net/
type logstashFormatter struct {
	Hostname string
}

func trimMessage(message string) string {
	if len(message) > maxMessageBodySize {
		message = message[:maxMessageBodySize] + " ... (message has been trimmed due to extreme length)"
	}
	return message
}

// We can not modify entry.Data as it is shared between multiple threads
func (f *logstashFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	dataCopy := map[string]interface{}{}
	for key, val := range entry.Data {
		dataCopy[key] = val
	}
	dataCopy["@version"] = 1
	dataCopy["HOSTNAME"] = f.Hostname
	dataCopy["@timestamp"] = entry.Time.UTC().Format(TimestampFormat)

	// set message field
	message := entry.Message
	if dataCopy[errorKey] != nil {
		message = fmt.Sprintf("%v: %v", message, dataCopy["error"])
	}
	dataCopy["message"] = trimMessage(message)

	if len(*flagLogstashLogTags) > 0 {
		dataCopy["@tags"] = strings.Join(*flagLogstashLogTags, ", ")
	}

	// Error is also a candidate for being large
	if dataCopy[errorKey] != nil {
		dataCopy[errorKey] = trimMessage(fmt.Sprintf("%v", dataCopy[errorKey]))
	}
	if dataCopy[errorStackKey] != nil {
		dataCopy[errorStackKey] = trimMessage(fmt.Sprintf("%v", dataCopy[errorStackKey]))
	}

	level := strings.ToUpper(entry.Level.String())
	// We assume that warning has level WARN inside kibana.
	if level == "WARNING" {
		level = "WARN"
	}
	dataCopy["level"] = level

	serialized, err := json.Marshal(dataCopy)
	if err != nil {
		return nil, fmt.Errorf("Failed to marshal fields to JSON, %v", err)
	}
	return append(serialized, '\n'), nil
}
