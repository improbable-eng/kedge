#TAG:16.04
FROM ubuntu@sha256:71cd81252a3563a03ad8daee81047b62ab5d892ebbfbf71cf53415f29c130950
MAINTAINER Improbable Team <infra@improbable.io>

ENV GOLANG_VERSION 1.8.1
ENV GOLANG_DOWNLOAD_URL https://golang.org/dl/go$GOLANG_VERSION.linux-amd64.tar.gz
ENV GITBRANCH master
ENV PATH /usr/local/go/bin:$PATH
ENV GOPATH=/go

RUN mkdir /etc/corp-auth

RUN apt-get update && apt-get install -qq -y --no-install-recommends git wget curl ca-certificates openssh-client

RUN curl -fsSL "${GOLANG_DOWNLOAD_URL}" -o golang.tar.gz \
      && tar -C /usr/local -xzf golang.tar.gz \
      && rm golang.tar.gz

RUN echo "StrictHostKeyChecking no" > /etc/ssh/ssh_config

# Install not vendored deps if any.
RUN go get github.com/sirupsen/logrus
RUN go get k8s.io/client-go/tools/clientcmd
RUN go get k8s.io/client-go/tools/clientcmd/api
RUN go get k8s.io/kubernetes/pkg/client/unversioned/clientcmd

# Copy local to not clone everything.
# NOTE: Make sure you have vendor installed using `git submodule update --init --recursive`
ADD . ${GOPATH}/src/github.com/mwitkow/kedge

ARG BUILD_VERSION
RUN echo "Installing Kedge with version ${BUILD_VERSION}"
RUN go install -ldflags "-X main.BuildVersion=${BUILD_VERSION}" github.com/mwitkow/kedge/server

ENTRYPOINT ["/go/bin/server"]