package ctxtags

const (

	// TagForAuth is used in authTripperware to specify auth.Source for backend auth.
	TagForAuth = "http.auth"
	// TagForProxyAuth is used in authTripperware to specify auth.Source for proxy auth.
	TagForProxyAuth = "http.proxy.auth"

	// TagForProxyDestURL is used in routeTripperware to specify proxy URL.
	TagForProxyDestURL = "http.proxy.url"

	// TagForProxyAdhoc is used in kedge proxy to specify adhoc rule used in request.
	TagForProxyAdhoc = "http.proxy.adhoc"
	// TagForProxyBackend is used in kedge proxy to specify backend used in request.
	TagForProxyBackend = "http.proxy.backend"
	// TagForScheme specifies which scheme request is using. It is specified by each server.
	TagForScheme = "http.scheme"

	// TagForProxyAuthTime specifies time that took to put valid proxy auth in Headers.
	// It can sometimes take time in case of full OIDC login.
	TagForProxyAuthTime = "http.proxy.auth.time"
	// TagForBackendAuthTime specifies time that took to put valid backend auth in Headers.
	// It can sometimes take time in case of full OIDC login.
	TagForBackendAuthTime = "http.auth.time"

	// TagForBackendTarget specifies the target name used to resolve in lbtransport by backend
	TagForBackendTarget = "http.backend.target"

	// TagForTargetAddress specifies the resolved address used by request in lbtransport.
	TagForTargetAddress = "http.target.address"

	// TagRequestID specified request ID of the request.
	TagRequestID = "http.request_id"
)
