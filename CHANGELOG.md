# Kedge & Winch Release Notes

### [v1.0.0-alpha.0](https://github.com/mwitkow/kedge/releases/tag/v0.0.1-alpha-test)
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