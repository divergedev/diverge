# Diverge AI Agent Context (AGENTS.md)

This document provides context and guidelines for AI coding assistants and agents working on the Diverge codebase.

## Project Overview

Diverge is a Kubernetes operator and environment-as-a-service engine that creates ephemeral preview environments triggered by merge request events. It supports delta deployments, configurable database provisioning, and Istio-based header routing.

## Architecture

The project consists of three main binaries:
1. **Controller (`cmd/controller`)**: A Kubernetes operator built with `controller-runtime`. It watches for `Environment` Custom Resources and provisions necessary resources (like Argo CD `Application` CRs, databases).
2. **Proxy (`cmd/proxy`)**: A reverse proxy that helps route traffic to specific preview environments based on headers.
3. **CLI (`cmd/diverge`)**: A command-line interface built with `cobra` for managing environments (`create`, `list`, `delete`, `open`, `logs`, `validate`, `status`, `version`, `init`).

## Key Packages

- `cmd/`: Entrypoints for the three binaries.
- `internal/cli/`: Implementation of the Cobra CLI commands.
- `internal/controller/`: K8s operator reconciliation loop
- `internal/notifier/`: GitLab/GitHub MR/PR comment notifications and commit status reporting
- `internal/webhook/`: Webhook handlers for GitLab/GitHub events
- `internal/proxy/`: Reverse proxy with header-based routing
- `internal/routing/`: Istio VirtualService and Gateway API routing
- `internal/argocd/`: ArgoCD Application generation (Helm + Kustomize)
- `internal/database/`: Database provisioning (shared, schema, snapshot, fresh) with SchemaProvider
- `internal/deployer/`: Deployer interface and ArgoCD deployer
- `internal/config/`: .diverge.yaml parsing with strict validation
- `internal/changeset/`: Git diff → changed service detection
- `api/v1alpha1/`: CRD types with OpenAPI validation
- `proto/diverge/v1alpha1/`: Protobuf definitions (environment.proto, service.proto)
- `gen/`: Generated protobuf-go, ConnectRPC stubs, and proto2type domain types
- `pkg/sdk/`: SDK for programmatic environment management
- `charts/diverge/`: The Helm chart for deploying Diverge.

## Build System

- **Nix**: We use Nix for managing development environments (`flake.nix`). **All terminal commands must be run inside the Nix dev shell by prefixing with `nix develop -c`.**
- **Make**: Standard targets include `make install` (install CRDs), `make run` (run operator locally), `make test`, `make build`, and `make proto` (generate protobuf/ConnectRPC/domain types).
- **Proto**: Uses `buf` for protobuf generation and `proto2type` for domain type generation. Run `make proto` after editing `.proto` files.
- **Go**: Version 1.26.

## Test Patterns

- We use `testify/assert` and `testify/require` for assertions.
- We favor **table-driven tests** for comprehensive coverage.
- We use `httptest` for testing HTTP servers/clients.
- We use Property-Based Testing (PBT) using `hegel` (`hegel.dev/go/hegel`) where specified or appropriate.
- Packages with property tests: `notifier`, `proxy`, `webhook`, `controller`, `routing`, `argocd`, `api/v1alpha1`, `database`.
- Currently passing 147 tests.

## Conventions

- **Linting**: Code must pass `golangci-lint run` and `go vet`.
- **Commits**: Use conventional commit messages (`feat: ...`, `fix: ...`, `docs: ...`). Do **NOT** use `--no-verify` with git commit.
- **Hooks**: We use `lefthook` for pre-commit hooks.
- **Idiomatic Go**: Write standard, idiomatic Go code with proper error handling.

## Security Conventions

- Sentinel errors (e.g., `ErrEnvironmentNotFound`)
- Context timeouts on all external calls
- Constant-time secret comparison for webhooks
- HeaderKey validation against RFC 7230
- SHA validation (hex-only regex) for commit status URLs
- Path traversal prevention in notifier API paths
- Label key/value validation using `k8s.io/apimachinery/pkg/util/validation`
- SQL injection prevention via regex-gated schema names (no parameterized DDL)
- Strict YAML unmarshaling (DisallowUnknownFields)
- DeepCopy baseline before status mutations

## CI/CD

- **GitHub Actions**: Workflows defined in `.github/workflows/` (e.g., `ci.yml`).
- **Goreleaser**: Used for building and releasing the project (`.goreleaser.yaml`). It builds the three binaries and packages them into a single consolidated Docker image (`ghcr.io/divergedev/diverge`).

## Open Issues / Coming Next

- **ConnectRPC API Server** (#12) — gRPC/ConnectRPC API server for environment management
- **WebSocket Support** (#6) — Full WebSocket proxying for real-time preview environments
- **Controller EnvTest + E2E** (#9) — Comprehensive controller integration tests with envtest
