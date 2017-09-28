package main

import (
	"bytes"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mwitkow/go-httpwares/metrics"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"
)

const (
	metricsPrefix = "kedge_loadtest"
)

var (
	clientStarted = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricsPrefix,
			Name:      "http_tripper_started_requests_total",
			Help:      "Count of started requests.",
		},
		[]string{"proxy", "target", "path", "method"},
	)
	clientCompleted = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricsPrefix,
			Name:      "http_tripper_completed_requests_total",
			Help:      "Count of completed requests.",
		},
		[]string{"proxy", "target", "path", "method", "status"},
	)
	clientLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: metricsPrefix,
			Name:      "http_tripper_completed_latency_seconds",
			Help:      "Latency of completed requests.",
			Buckets:   []float64{.01, .03, .1, .3, .7, 1, 3, 5, 7, 8, 10, 15, 30},
		},
		[]string{"proxy", "target", "path", "method", "status"},
	)
)

func init() {
	prometheus.MustRegister(clientStarted)
	prometheus.MustRegister(clientCompleted)
	prometheus.MustRegister(clientLatency)
}

func printStats() error {
	fmt.Println("LOAD-TEST STATS:")
	metrics, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		return errors.Wrap(err, "failed to Gather metrics")
	}

	contentType := expfmt.FmtText
	buf := &bytes.Buffer{}
	enc := expfmt.NewEncoder(buf, contentType)
	for _, mf := range metrics {
		if !strings.HasPrefix(mf.GetName(), metricsPrefix) {
			continue
		}

		if err := enc.Encode(mf); err != nil {
			return errors.Wrapf(err, "failed to Encode metric metric family %s", mf.GetName())
		}
	}

	fmt.Println(buf.String())
	return nil
}

type reporter struct {
	proxyAddress string
}

type meta struct {
	proxyAddress, host, path, method string
}

func reqMeta(req *http.Request, proxyAddr string) *meta {
	m := &meta{
		proxyAddress: proxyAddr,
		path:         req.URL.Path,
		method:       req.Method,
	}
	m.host = req.URL.Host
	if m.host == "" {
		m.host = req.Host
	}
	return m
}

func (r *reporter) Track(req *http.Request) http_metrics.Tracker {
	return &tracker{
		meta: reqMeta(req, r.proxyAddress),
	}
}

type tracker struct {
	*meta
}

func (t *tracker) RequestStarted() {
	clientStarted.WithLabelValues(t.proxyAddress, t.host, t.path, t.method).Inc()
}

func (t *tracker) RequestRead(duration time.Duration, size int) {}

func (t *tracker) ResponseStarted(duration time.Duration, code int, header http.Header) {
	status := strconv.Itoa(code)
	clientCompleted.WithLabelValues(t.proxyAddress, t.host, t.path, t.method, status).Inc()
	clientLatency.WithLabelValues(t.proxyAddress, t.host, t.path, t.method, status).Observe(duration.Seconds())
}

func (t *tracker) ResponseDone(duration time.Duration, code int, size int) {}
