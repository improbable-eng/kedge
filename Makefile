PREFIX            ?= $(shell pwd)
FILES             ?= $(shell find . -type f -name '*.go' -not -path "./vendor/*")
DOCKER_IMAGE_NAME ?= kedge
DOCKER_IMAGE_TAG  ?= $(subst /,-,$(shell git rev-parse --abbrev-ref HEAD))

all: install-tools format build

format:
	@echo ">> formatting code"
	@goimports -w $(FILES)

deps: install-tools
	@echo ">> downloading dependencies"
	@dep ensure

vet:
	@echo ">> vetting code"
	@go vet ./...

install-tools:
	@echo ">> fetching goimports"
	@go get -u golang.org/x/tools/cmd/goimports
	@echo ">> fetching dep"
	@go get -u github.com/golang/dep/cmd/dep

proto:
	@echo ">> generating protobufs"
	@go get github.com/mwitkow/go-proto-validators/protoc-gen-govalidators
	@go get github.com/golang/protobuf/protoc-gen-go
	@./scripts/protogen.sh

test:
	@echo ">> running all tests"
	@go test $(shell go list ./... | grep -v /vendor/)

docker:
	@echo ">> building docker image"
	@docker build --build-arg BUILD_VERSION=$(date +%Y%m%d-%H%M%S)-001 -t "$(DOCKER_IMAGE_NAME):$(DOCKER_IMAGE_TAG)" .

.PHONY: all format deps vet install-tools proto test docker