#TAG:16.04
FROM ubuntu@sha256:71cd81252a3563a03ad8daee81047b62ab5d892ebbfbf71cf53415f29c130950
MAINTAINER Improbable Team <infra@improbable.io>

ENV GOLANG_VERSION 1.8.1
ENV GOLANG_DOWNLOAD_URL https://golang.org/dl/go$GOLANG_VERSION.linux-amd64.tar.gz
ENV GITBRANCH master
ENV PATH /usr/local/go/bin:$PATH
ENV GOPATH=/go
ENV GOBIN=/go/bin

RUN mkdir /etc/corp-auth

RUN apt-get update && apt-get install -qq -y --no-install-recommends git vim wget curl ca-certificates openssh-client

RUN curl -fsSL "${GOLANG_DOWNLOAD_URL}" -o golang.tar.gz \
      && tar -C /usr/local -xzf golang.tar.gz \
      && rm golang.tar.gz

RUN echo "StrictHostKeyChecking no" > /etc/ssh/ssh_config

ENV PATH ${PATH}:${GOBIN}
RUN mkdir -p /go/bin
RUN wget -O ${GOBIN}/dep "https://github.com/golang/dep/releases/download/v0.3.2/dep-linux-amd64" && chmod +x ${GOBIN}/dep
# Copy local to not clone everything.
ADD . ${GOPATH}/src/github.com/improbable-eng/kedge
RUN cd ${GOPATH}/src/github.com/improbable-eng/kedge && dep ensure

ARG BUILD_VERSION
RUN echo "Installing LoadTester"
RUN go install github.com/improbable-eng/kedge/tools/loadtest

# This image is designed to be run e.g as a pod in separate k8s cluster to target some kedge for load test.
# This container does not include winch, so make sure to configure loadtest against kedge directly.
ENTRYPOINT ["bash"]