package metrics

import "github.com/prometheus/client_golang/prometheus"

const (
	ConfiguationActionCreate = "create"
	ConfiguationActionChange = "update"
	ConfiguationActionDelete = "delete"
)
var (
	BackendHTTPConfigurationCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kedge_http_backend_configuration_changes_total",
			Help: "Count of changes in HTTP backend configuration, done by flagz flag change. It can be both because use changed dynamic flag" +
				"or dynamic routing discovered a change",
		},
		[]string{"backend_name", "action"},
	)
	BackendGRPCConfigurationCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "kedge_grpc_backend_configuration_changes_total",
			Help: "Count of changes in gRPC backend configuration, done by flagz flag change. It can be both because use changed dynamic flag" +
				"or dynamic routing discovered a change",
		},
		[]string{"backend_name", "action"},
	)
)

func init() {
	prometheus.MustRegister(BackendHTTPConfigurationCounter)
	prometheus.MustRegister(BackendGRPCConfigurationCounter)
}