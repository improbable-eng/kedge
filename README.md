# :anchor: kedge - Kubernetes Edge Proxy

[![Travis Build](https://travis-ci.org/mwitkow/kedge.svg?branch=master)](https://travis-ci.org/mwitkow/kedge)
[![Go Report Card](https://goreportcard.com/badge/github.com/mwitkow/kedge)](https://goreportcard.com/report/github.com/mwitkow/kedge)
[![GoDoc](http://img.shields.io/badge/GoDoc-Reference-blue.svg)](https://godoc.org/github.com/mwitkow/grpc-proxy)
[![Apache 2.0 License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![quality: WIF](https://img.shields.io/badge/quality-WIP-red.svg)](#status)

 > [kedge](https://www.merriam-webster.com/dictionary/kedge) (verb) to move (a ship) by means of a line attached to a small anchor dropped at the distance and in the direction desired

Kubernetes Frontend (KFE) for gRPC, HTTP (1.1/2) microservices with the aim to make cross-cluster
microservice communication simple to set up, and secure. All you need for it to work is: TLS client certificates in your service pods and a single L4 load balanced IP address in each cluster.

## The pain of cross-cluster Kubernetes

Kubernetes is great, if you have one cluster. If you want to have twoThis project stems from the frustration of setting up communication between two K8S clusters. This requires a couple of things:
 - cross-cluster networking - usually a complex process of setting up and maintaining IPSec bridges
 - configuration of routing rules - each cluster needs to know about each other cluster's 3 (!) network ranges: host, pod and internal-service networks
 - providing federated service discovery - either through the alpha-grade [K8S Federation](https://kubernetes.io/docs/concepts/cluster-administration/federation/) or [CoreDNS](https://github.com/coredns/coredns) stub zones
All these are subject to subtle interplays between routes, `iptables` rules, DNS packets and MTU limits of IPSec tunnels, which would make even a seasoned network engineer go gray.

At the same time, none of the existing service meshes or networking overlays provide an easy fix for this.

## Kedge Design

Kedge is a reverse/forward proxy for gRPC and HTTP traffic. 

It uses a concept of *backends* (see [gRPC](proto/kedge/config/grpc/backends/backend.proto), [HTTP](kedge/config/http/backends/backend.proto)) that map onto K8S [`Services`](https://kubernetes.io/docs/user-guide/services/). These define load balancing policies, middleware used for calls, and resolution. The backends have "warm" connections ready to receive inbound requests.

The inbound requests are directed to *backends* based on *routes* (see [gRPC](proto/kedge/config/grpc/routes/routes.proto), [HTTP](proto/kedge/config/grpc/routes/routes.proto)). These match onto requests based on host, paths (services), headers (metadata). They also specify authorization requirements for the route to be taken.

## Usage

Please see the [server](server/) readme for an actual guide.

## Status

The project is very much **work in progress**. Experimentation is recommended, usage in production rather not. The following features and items are planned:

Kedge Service:
 * [x] - gRPC(S) backend definitions and backend pool - SRV discovery and RR LB
 * [x] - gRPC(S) proxying based on routes (service, authority) to defined backends
 * [x] - HTTP(S) backend definitions and backend pool - SRV disovery and RR LB
 * [x] - HTTP(S) proxying based on routes (path, host) to defined backends
 * [x] - integration tests for HTTP, gRPC proxying (backend and routing)
 * [x] - TLS client-certificate verification based off CA chains
 * [ ] - example Kubernetes YAML files (deployment, config maps)
 * [ ] - TLS configuration (CA chains, etc.) for gRPC and HTTP backends 
 * [x] - support for Forward Proxying and Reverse Proxying in HTTP backends
 * [ ] - "adhoc routes" - support for HTTP Forward Proxying to an arbitrary (but filtered) SRV destination without a backend - calling pods
 * [ ] - support for K8S auto-discovery of service backends based off metadata
 * [ ] - support for TLS client certificate authentication on routes (metadata matches)
 * [ ] - support for OpenID JWT token authentication on routes (claim matches) - useful for proxying to Kubernetes API Server
 * [ ] - support for load balanced CONNECT method proxying for TLS passthrough to backends - if needed
 
Kedge Client:
 * [ ] - matching logic for "remap something.my_cluster.cluster.local to my_cluster.internalapi.example.com" for finding Kedges on the internet
 * [ ] - reading of TLS client certs from ~/.config/kedge
 * [ ] - Forward Proxy to remote Kedges for a CLI command (setting HTTP_PROXY) "kedge_local <cmd>"
 * [ ] - Forward Proxy in daemon mode with an auto-gen [PAC](https://en.wikipedia.org/wiki/Proxy_auto-config) file
 


## License

`kedge` is released under the Apache 2.0 license. See [LICENSE.txt](LICENSE.txt).

