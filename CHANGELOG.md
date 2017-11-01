# Kedge & Winch Release Notes

### [v1.0.0-beta.4](https://github.com/mwitkow/kedge/releases/tag/v1.0.0-beta.4)
Kedge service:
* [x] added dynamic routing discovery for TLS routes (insecure) 

### [v1.0.0-beta.3](https://github.com/mwitkow/kedge/releases/tag/v1.0.0-beta.3)
Kedge service:
* [x] added stripping out proxy auth header after using it.
* [x] fixed error handling causing in particular cases.
* [x] added graceful shutdown 

Winch (kedge client):
* [x] better error handling (adding response headers to indicate what error happen)
* [x] CORS

Tools: 
* [x] added load tester.

### [v1.0.0-beta.2](https://github.com/mwitkow/kedge/releases/tag/v1.0.0-beta.2)
Kedge service:
* [x] added reported helping to determine proxy errors from backend errors (producing log and inc metric)
* [x] added support winch debug mode
* [x] added support for request ID
* [x] fixed go routine leaks on discovery and k8sresolver streams
* [x] improved logging on discovery logic
* [x] fixed go routine leaks on lbtransport
* [x] dynamic discovery changes are less disruptive 

Winch (kedge client):
* [x] added debug mode
* [x] added request ID

### [v1.0.0-beta.1](https://github.com/mwitkow/kedge/releases/tag/v1.0.0-beta.1)
Kedge service:
* [x] added metrics for backend configuration change
* [x] added metrics for HTTP requests/response to middleware and from tripperware
* [x] updated go-httpares dep  

### [v1.0.0-beta.0](https://github.com/mwitkow/kedge/releases/tag/v1.0.0-beta.0)
Kedge service:
* [x] added support for K8S auto-discovery of service backends based off metadata (no need to actually specify routes manually!)
* [x] fixed retry backoff bug in lbtransport
* [x] added test log resolution
* [x] logging improvements

Winch (kedge client):
* [x]  fixed handling of debug endpoints.

### [v1.0.0-alpha.3](https://github.com/mwitkow/kedge/releases/tag/v1.0.0-alpha.3)
Kedge Service:
* [x] - fixed remote logging
* [x] - moved to glide as vendoring tool
* [x] - added support for specifying port for director routes
* [x] - added support for overwriting port on SRV lookup
* [x] - implemented fully equipped k8sresolver (basing on k8s endpoints API)
* [x] - updated OIDC library with patch
* [x] - improved debuggability, passed proper logger with corresponded tags everywhere
* [x] - removed Trial dialing in favor of better error handling

Winch (kedge client):
* [x] - various improvements for passing auth as well as addition for new auth types
* [x] - added port matching on winch
* [x] - various fixes for templating

### [v1.0.0-alpha.2](https://github.com/mwitkow/kedge/releases/tag/v1.0.0-alpha.2)
Kedge Service:
* [x] - add support for specifying whitelist or required permissions in ID Token for OpenID provider. 

Winch (kedge client):
* [x] - support more auth providers and kinds (bearertoken & gcp from kube/config)

### [v1.0.0-alpha.1](https://github.com/mwitkow/kedge/releases/tag/v1.0.0-alpha.1)
Kedge Service:
* [x] - added optional remote logging to logstash

### [v1.0.0-alpha.0](https://github.com/mwitkow/kedge/releases/tag/v1.0.0-alpha.0)
Initial release to start testing on real clusters.

Kedge Service:
* [x] - gRPC(S) backend definitions and backend pool - SRV discovery and RR LB
* [x] - gRPC(S) proxying based on routes (service, authority) to defined backends
* [x] - HTTP(S) backend definitions and backend pool - SRV disovery and RR LB
* [x] - HTTP(S) proxying based on routes (path, host) to defined backends
* [x] - integration tests for HTTP, gRPC proxying (backend and routing)
* [x] - TLS client-certificate verification based off CA chains
* [x] - support for Forward Proxying and Reverse Proxying in HTTP backends
* [x] - support for OpenID JWT token authentication on routes (claim matches) - useful for proxying to Kubernetes API Server

Winch (kedge client):
* [x] - HTTP forward Proxy to remote Kedges for a CLI applications (setting HTTP_PROXY).
* [x] - HTTP forward Proxy in daemon mode for browsers with an auto-gen [PAC](https://en.wikipedia.org/wiki/Proxy_auto-config) file.
* [x] - matching logic for "remap something.my_cluster.cluster.local to my_cluster.internalapi.example.com" for finding Kedges on the internet
* [x] - open ID connect login to get ID token / refresh token
* [x] - support for custom root CA for TLS with kedge