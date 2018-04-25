# Kedge & Winch Release Notes

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](http://keepachangelog.com/en/1.0.0/)
and this project adheres to [Semantic Versioning](http://semver.org/spec/v2.0.0.html).

Types of changes:
`Added` for new features.
`Changed` for changes in existing functionality.
`Deprecated` for soon-to-be removed features.
`Removed` for now removed features.
`Fixed` for any bug fixes.
`Security` in case of vulnerabilities.

## [Unreleased]
### Added
- kedge: gRPC adhoc!
### Fixed
- winch: Fixed go routine leaks in gRPC path (client connection not closed)
- winch: Allow Debug endpoints to be exposed on different port.

## [0.1.0](https://github.com/improbable-eng/kedge/releases/tag/v0.1.0) - 2018-04-13
### Added
- winch: Early error check if user tries to connect to IP instead of hostname
### Changed
- kedge: Improved not-kedge-destination error message.
### Fixed
- kedge: More reliable even stream for k8sresolver.


Old Releases (format not applicable)
### [v1.0.0-beta.12](https://github.com/improbable-eng/kedge/releases/tag/v1.0.0-beta.12)
Kedge service:
* [x] Fixed critical bug(s) in k8sresolver

Tools:
* [x] Added standalone k8sresolver runner for debugging purposes.

### [v1.0.0-beta.10](https://github.com/improbable-eng/kedge/releases/tag/v1.0.0-beta.10)
Kedge service:
* [x] Added support for [grpc-web](https://github.com/improbable-eng/grpc-web) protocol

Winch (kedge client):
* [x] Added new OIDC-based auth method with service accounts.

### [v1.0.0-beta.9](https://github.com/improbable-eng/kedge/releases/tag/v1.0.0-beta.9)
Kedge service:
* [x] Kubernetes discovery now prepends service short name to route matcher instead of just service name.

### [v1.0.0-beta.8](https://github.com/improbable-eng/kedge/releases/tag/v1.0.0-beta.8)
Kedge service:
* [x] Fixed passing headers through gRPC proxies
* [x] Updated Docs!
* [x] Better error handling
* [x] Fixed not working gRPC authority matcher
* [x] Fixed and tested HostResolver
* [x] Added way to change metric endpoint route

Winch (kedge client):
* [x] Updated Docs!

### [v1.0.0-beta.5](https://github.com/improbable-eng/kedge/releases/tag/v1.0.0-beta.5)
Kedge service:
* [x] added OIDC support to gRPC flow

Winch (kedge client):
* [x] added gRPC support to winch

### [v1.0.0-beta.4](https://github.com/improbable-eng/kedge/releases/tag/v1.0.0-beta.4)
Kedge service:
* [x] added dynamic routing discovery for TLS routes (insecure) 

### [v1.0.0-beta.3](https://github.com/improbable-eng/kedge/releases/tag/v1.0.0-beta.3)
Kedge service:
* [x] added stripping out proxy auth header after using it.
* [x] fixed error handling causing in particular cases.
* [x] added graceful shutdown 

Winch (kedge client):
* [x] better error handling (adding response headers to indicate what error happen)
* [x] CORS

Tools: 
* [x] added load tester.

### [v1.0.0-beta.2](https://github.com/improbable-eng/kedge/releases/tag/v1.0.0-beta.2)
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

### [v1.0.0-beta.1](https://github.com/improbable-eng/kedge/releases/tag/v1.0.0-beta.1)
Kedge service:
* [x] added metrics for backend configuration change
* [x] added metrics for HTTP requests/response to middleware and from tripperware
* [x] updated go-httpares dep  

### [v1.0.0-beta.0](https://github.com/improbable-eng/kedge/releases/tag/v1.0.0-beta.0)
Kedge service:
* [x] added support for K8S auto-discovery of service backends based off metadata (no need to actually specify routes manually!)
* [x] fixed retry backoff bug in lbtransport
* [x] added test log resolution
* [x] logging improvements

Winch (kedge client):
* [x]  fixed handling of debug endpoints.

### [v1.0.0-alpha.3](https://github.com/improbable-eng/kedge/releases/tag/v1.0.0-alpha.3)
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

### [v1.0.0-alpha.2](https://github.com/improbable-eng/kedge/releases/tag/v1.0.0-alpha.2)
Kedge Service:
* [x] - add support for specifying whitelist or required permissions in ID Token for OpenID provider. 

Winch (kedge client):
* [x] - support more auth providers and kinds (bearertoken & gcp from kube/config)

### [v1.0.0-alpha.1](https://github.com/improbable-eng/kedge/releases/tag/v1.0.0-alpha.1)
Kedge Service:
* [x] - added optional remote logging to logstash

### [v1.0.0-alpha.0](https://github.com/improbable-eng/kedge/releases/tag/v1.0.0-alpha.0)
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
