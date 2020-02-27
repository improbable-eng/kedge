FROM golang:1.13 as build
MAINTAINER Improbable Team <infra@improbable.io>

ADD . ${GOPATH}/src/github.com/improbable-eng/kedge
WORKDIR ${GOPATH}/src/github.com/improbable-eng/kedge

ARG BUILD_VERSION
RUN echo "Installing Kedge with version ${BUILD_VERSION}"
RUN go install -ldflags "-X main.BuildVersion=${BUILD_VERSION}" github.com/improbable-eng/kedge/cmd/kedge
RUN go install -ldflags "-X main.BuildVersion=${BUILD_VERSION}" github.com/improbable-eng/kedge/cmd/winch

FROM ubuntu:18.04

RUN apt-get update && apt-get install -qq -y --no-install-recommends git wget curl ca-certificates openssh-client

RUN mkdir /etc/corp-auth
RUN echo "StrictHostKeyChecking no" > /etc/ssh/ssh_config

COPY --from=build /go/bin/winch /go/bin/kedge /

ENTRYPOINT ["/kedge"]

