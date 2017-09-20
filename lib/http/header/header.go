package header

const (
	// KedgeRequestID header is used to specify RequestID.
	KedgeRequestID = "X-Kedge-Request-ID"

	// KedgeForceInfoLogs header is used to signal kedge, to print all logs DEBUG logs for this request as INFO.
	KedgeForceInfoLogs ="X-Kedge-Info-Logs"
)