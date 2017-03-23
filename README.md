# KFE Kubernetes FrontEnd

[![Travis Build](https://travis-ci.org/mwitkow/grpc-proxy.svg?branch=master)](https://travis-ci.org/mwitkow/grpc-proxy)
[![Go Report Card](https://goreportcard.com/badge/github.com/mwitkow/grpc-proxy)](https://goreportcard.com/report/github.com/mwitkow/grpc-proxy)
[![GoDoc](http://img.shields.io/badge/GoDoc-Reference-blue.svg)](https://godoc.org/github.com/mwitkow/grpc-proxy)
[![Apache 2.0 License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

Kubernetes Frontend (KFE) for gRPC, HTTP (1.1/2) microservices with the aim to make cross-cluster
microservice communication simple (no service discovery) and secure (TLS client certs).


## Reason

Kubernetes is great, if you have one cluster. If you have two, you face the problem of making service in cluster A 
communicate with a service in cluster B. [K8S Federation](https://kubernetes.io/docs/concepts/cluster-administration/federation/) and others
networking solutions rely on low-level networking that is hard to set up (tunnels, DNS federation).

This project allows for a really simple solution to the problem: a FE that allows services to talk to others services
via client-side certificates.

## Project Status

**In development**


## License

`kfe` is released under the Apache 2.0 license. See [LICENSE.txt](LICENSE.txt).

