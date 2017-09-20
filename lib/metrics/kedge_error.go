package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	KedgeProxyErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kedge_proxy_errors_total",
			Help: "Count of errors spotted during proxying request. These errors are fails that never went outside of kedge.",
		},
		[]string{"backend_name", "type"},
	)
)

func init() {
	prometheus.MustRegister(KedgeProxyErrors)
}