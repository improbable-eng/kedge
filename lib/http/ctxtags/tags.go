package ctxtags

const (
	// TagForAuth is used in authTripperware to specify auth.Source for backend auth.
	TagForAuth = "http.auth"
	// TagForProxyAuth is used in authTripperware to specify auth.Source for proxy auth.
	TagForProxyAuth = "http.proxy.auth"

	// TagForProxyDestURL is used in routeTripperware to specify proxy URL.
	TagForProxyDestURL = "http.proxy.url"
)
