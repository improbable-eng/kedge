PREFIX            ?= $(shell pwd)
FILES             ?= $(shell find . -type f -name '*.go' -not -path "./vendor/*")
DOCKER_IMAGE_NAME ?= kedge
DOCKER_IMAGE_TAG  ?= $(subst /,-,$(shell git rev-parse --abbrev-ref HEAD))

all: build

format:
	@echo ">> formatting code"
	@goimports -w $(FILES)

deps: install-tools
	@echo ">> downloading dependencies"
	@go mod download

build:
	@echo ">> building kedge"
	@go install github.com/improbable-eng/kedge/cmd/kedge
	@echo ">> building winch"
	@go install github.com/improbable-eng/kedge/cmd/winch

vet:
	@echo ">> vetting code"
	@go vet ./...

install-tools:
	@echo ">> fetching goimports"
	@go get -u golang.org/x/tools/cmd/goimports

proto: 
	@echo ">> generating protobufs"
	@./scripts/protogen.sh

test: build
	@echo ">> running all tests"
	@go test $(shell go list ./... | grep -v /vendor/)

docker:
	@echo ">> building docker image"
	@docker build --build-arg BUILD_VERSION=$(date +%Y%m%d-%H%M%S)-001 -t "$(DOCKER_IMAGE_NAME):$(DOCKER_IMAGE_TAG)" .

.PHONY: all format deps build vet install-tools proto test docker
