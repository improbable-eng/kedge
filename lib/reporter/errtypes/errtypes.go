package errtypes

type Type string

const (
	OK Type = ""

	// Unauthorized is an error returned by proxy.AuthMiddleware indicating case when request is not authorized to be proxied.
	// NOTE: This is only for OIDC auth. Cert auth is done on http.Server level, and there is no reporting implemented yet on that.
	Unauthorized Type = "unauthorized"

	// NoRoute is an error returned by p.router.Route(req) indicating no route for given request.
	NoRoute Type = "no-route"

	// RouteUnknownError is an error returned by p.router.Route(req) indicating some unknown error than no route.
	RouteUnknownError Type = "unknown-route-error"

	// NoBackend is the only error that can be returned by backendpool.Tripper (ErrUnknownBackend)
	// It can happen on bug or wrong configuration (routing exists for not existing backend) or race in configuration.
	NoBackend Type = "no-backend"

	// BackendTransportClosed is an error returned by backendpool.closedTripper indicating that the backend should
	// not be used, but somehow it was still in usage.
	BackendTransportClosed Type = "backend-transport-closed"

	// NoConnToAllResolvedAddresses is an error returned by lbtransport.tripper when all addresses (IP:Port) returned by
	// our resolver (K8s or DNS) are not accessible (dial errors). This can happen for DNS when DNS itself is broken.
	NoConnToAllResolvedAddresses Type = "no-connection-to-all-resolved-addresses"

	// NoResolutionAvailable is an error returned by lbtransport.tripper when we have an resolution error constantly and there
	// is no (even old) resolution, so no target to even try.
	NoResolutionAvailable Type = "no-resolution-available"

	// ConnErr is an error returned by reporter itself if the roundTrip fails without lastSeen error.
	ConnErr Type = "conn-err"
)
