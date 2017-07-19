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
	// TagsForProxyBackend is used in kedge proxy to specify backend used in request.
	TagsForProxyBackend = "http.proxy.backend"
	// TagsForScheme specifies which scheme request is using. It is specified by each server.
	TagsForScheme = "http.scheme"
)
