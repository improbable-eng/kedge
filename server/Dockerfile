FROM golang:1.8
ADD . /build
WORKDIR /build
RUN go get -v -d
RUN go build -o /kedge
WORKDIR /
RUN rm -r /build
ENTRYPOINT ["/kedge"]
