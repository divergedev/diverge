# Image URL to use all building/pushing image targets
IMG ?= divergedev/diverge:latest

.PHONY: all
all: build

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	echo "Mocking manifests..."

.PHONY: proto
proto: ## Generate protobuf Go types, ConnectRPC stubs, and domain types.
	buf generate
	buf generate --template buf.gen.domain.yaml
	@# Remove domain types for diverge/v1 (propagation.proto uses non-module import path)
	rm -rf gen/domain/diverge/v1

.PHONY: generate
generate: proto ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	echo "Mocking generate..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: lint
lint: ## Run golangci-lint
	golangci-lint run

.PHONY: test
test: manifests generate fmt vet ## Run tests.
	go test ./... -coverprofile cover.out

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	go build -o bin/diverge-controller cmd/controller/main.go

.PHONY: build-all
build-all: build build-cli build-proxy ## Build all binaries.

.PHONY: build-cli
build-cli: fmt vet ## Build diverge CLI binary.
	go build -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT)" -o bin/diverge cmd/diverge/main.go

.PHONY: build-proxy
build-proxy: fmt vet ## Build diverge proxy binary.
	go build -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT)" -o bin/diverge-proxy cmd/proxy/main.go

.PHONY: install-cli
install-cli: ## Install diverge CLI to GOPATH.
	go install ./cmd/diverge

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd/controller/main.go

.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	docker build -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	docker push ${IMG}

##@ Deployment

.PHONY: install
install: manifests ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	echo "Installing CRDs..."

.PHONY: release-snapshot
release-snapshot: ## Run GoReleaser locally (snapshot)
	goreleaser release --snapshot --clean
