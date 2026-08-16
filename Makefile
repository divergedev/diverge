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
	controller-gen crd paths=./api/... output:crd:dir=config/crd/bases

.PHONY: proto
proto: ## Generate protobuf code
	buf lint
	buf generate

.PHONY: generate
generate: proto ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	controller-gen object paths=./api/...

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

.PHONY: e2e-setup e2e-run e2e-teardown e2e

e2e-setup: ## Create Kind cluster and install CRDs
	kind get clusters | grep -q diverge-e2e || kind create cluster --name diverge-e2e --config test/e2e/kind-config.yaml
	$(MAKE) docker-build
	kind load docker-image divergedev/diverge:latest --name diverge-e2e
	kubectl apply -f config/crd/bases/ --context kind-diverge-e2e
	kubectl apply -k config/default --context kind-diverge-e2e || true
	kubectl -n diverge-system wait --for=condition=available deployment/diverge-controller --timeout=60s --context kind-diverge-e2e || true

e2e-run: ## Run E2E tests
	go test -tags=e2e -v -count=1 -timeout=10m ./test/e2e/...

e2e-teardown: ## Delete Kind cluster
	kind delete cluster --name diverge-e2e

e2e: e2e-setup ## Full E2E cycle
	@$(MAKE) e2e-run; \
	status=$$?; \
	$(MAKE) e2e-teardown; \
	exit $$status

e2e-dual-setup:
	nix develop -c ./test/e2e/setup_dual.sh

e2e-dual:
	nix develop -c go test -tags=e2e_dual -v -count=1 -timeout=10m ./test/e2e/...

e2e-dual-teardown:
	nix develop -c ./test/e2e/teardown_dual.sh

e2e-istio:
	nix develop -c go test -tags=e2e_istio -v -count=1 -timeout=15m ./test/e2e/...

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	go build -o bin/diverge-controller cmd/controller/main.go

.PHONY: build-slim
build-slim: ## Build slim manager binary without heavy providers
	nix develop -c go build -tags=no_knative,no_schema -o /tmp/diverge-controller-slim ./cmd/controller/

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

##@ Demo

.PHONY: demo demo-setup demo-teardown demo-killer demo-scenario-1 demo-scenario-2 demo-scenario-3 demo-scenario-4

demo: demo-setup
	@echo ""
	@echo "🔀 Diverge Demo Ready!"
	@echo "═══════════════════════"
	@echo "Run scenarios:"
	@echo "  make demo-killer      # 🪦 Telepresence is Dead (headline demo)"
	@echo "  make demo-scenario-1  # Preview routing"
	@echo "  make demo-scenario-2  # GAMMA mesh routing"
	@echo "  make demo-scenario-3  # Collision detection"
	@echo "  make demo-scenario-4  # Cleanup & dead man's switch"
	@echo ""
	@echo "Teardown: make demo-teardown"

demo-setup:
	@bash demo/setup.sh

demo-teardown:
	@bash demo/teardown.sh

demo-killer: ## 🪦 The "Telepresence is Dead" headline demo
	@bash demo/scenarios/00-telepresence-killer.sh

demo-scenario-1:
	@bash demo/scenarios/01-preview-routing.sh

demo-scenario-2:
	@bash demo/scenarios/02-gamma-mesh.sh

demo-scenario-3:
	@bash demo/scenarios/03-collision-detection.sh

demo-scenario-4:
	@bash demo/scenarios/04-cleanup.sh

.PHONY: demo-gke demo-gke-setup demo-gke-teardown demo-gke-killer

GKE_CTX = gke_$(GCP_PROJECT)_$(or $(GCP_REGION),us-central1)_$(or $(GKE_CLUSTER),diverge-demo)

demo-gke: demo-gke-setup ## Deploy demo to GKE Autopilot (requires GCP_PROJECT)

demo-gke-setup:
	@bash demo/setup-gke.sh

demo-gke-teardown: ## Destroy GKE demo cluster
	@bash demo/teardown-gke.sh

demo-gke-killer: ## Run headline demo on GKE (requires GCP_PROJECT)
ifndef GCP_PROJECT
	$(error GCP_PROJECT is required. Usage: GCP_PROJECT=my-project make demo-gke-killer)
endif
	@DIVERGE_DEMO_CTX=$(GKE_CTX) bash demo/scenarios/00-telepresence-killer.sh
