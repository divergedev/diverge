# Diverge AI Agent Context (AGENTS.md)

This document provides context and guidelines for AI coding assistants and agents working on the Diverge codebase.

## Project Overview

Diverge is an open-source Kubernetes operator and environment-as-a-service platform that creates ephemeral preview environments triggered by merge request events. Core capabilities include delta deployments, composable environments with service topology, async routing (Kafka, Temporal, Webhook), configurable database provisioning, header-based and subdomain routing, a ConnectRPC API server with OIDC authentication, and an MCP server for AI agent integration.

## Architecture

The project consists of four main binaries:
1. **Controller (`cmd/controller`)**: A Kubernetes operator built with `controller-runtime`. Watches `Environment` and `PreviewGroup` CRDs, provisions resources (routing, databases, ArgoCD apps, async infra).
2. **Server (`cmd/server`)**: ConnectRPC API server with OIDC + K8s TokenReview dual auth, real-time streaming, dashboard UI, and MCP server.
3. **Proxy (`cmd/proxy`)**: A reverse proxy for header-based and subdomain routing to preview environments.
4. **CLI (`cmd/diverge`)**: Command-line interface built with `cobra` for managing environments (`create`, `list`, `delete`, `dev`, `diff`, `route`, `graph`, `mcp`, `validate`, `status`, etc.).

Slim builds are available with build tags: `no_knative`, `no_schema`, `no_temporal`, `no_kafka`.

## Key Packages

- `cmd/`: Entrypoints for the four binaries.
- `internal/cli/`: Implementation of the Cobra CLI commands.
- `internal/controller/`: K8s operator reconciliation loop.
- `internal/server/`: ConnectRPC server, auth, dashboard, streaming.
- `internal/server/auth/`: OIDC, GitHub, TokenReview authentication with CompositeProvider.
- `internal/server/dashboard/`: Embedded React dashboard (Vite + TypeScript).
- `internal/notifier/`: GitLab/GitHub MR/PR comment notifications and commit status reporting.
- `internal/webhook/`: Webhook handlers for GitLab/GitHub events.
- `internal/proxy/`: Reverse proxy with header-based and subdomain routing.
- `internal/routing/`: Gateway API HTTPRoute generation (header + subdomain modes).
- `internal/argocd/`: ArgoCD Application generation (Helm + Kustomize).
- `internal/database/`: Database provisioning (shared, schema, snapshot, fresh) with SchemaProvider.
- `internal/deployer/`: Deployer interface, ArgoCD deployer, KEDA ScaledObject generation.
- `internal/config/`: `.diverge.yaml` parsing with strict validation (`KnownFields(true)`).
- `internal/changeset/`: Git diff → changed service detection for `diverge diff`.
- `internal/async/`: Async routing providers (Kafka, Temporal, Webhook) + provisioner.
- `internal/metrics/`: Prometheus metrics (reconciliation, deployments, routing, environments).
- `internal/events/`: Event system.
- `pkg/topology/`: Service graph resolution (static, Gateway API, Prometheus discovery).
- `pkg/sdk/`: SDK for programmatic environment management (HTTP, gRPC, Connect, Kafka).
- `api/v1alpha1/`: CRD types with OpenAPI validation.
- `proto/diverge/v1alpha1/`: Protobuf definitions.
- `gen/`: Generated protobuf-go, ConnectRPC stubs, and proto2type domain types.
- `web/`: React dashboard (Vite + TypeScript). Proto files are gitignored — run `buf generate --template buf.gen.web.yaml`.
- `charts/diverge/`: Helm chart for deploying Diverge.

## Build System

- **Nix**: We use Nix for managing development environments (`flake.nix`). **All terminal commands must be run inside the Nix dev shell by prefixing with `nix develop -c`.** This includes `git commit` (for pre-commit hooks).
- **Make**: Standard targets include `make install` (install CRDs), `make run` (run operator locally), `make test`, `make build`, `make generate` and `make manifests` (controller-gen), and `make proto` (generate protobuf/ConnectRPC/domain types).
- **Proto**: Uses `buf` for protobuf generation and `proto2type` for domain type generation. Run `make proto` after editing `.proto` files.
- **Go**: Version 1.26.
- **Dashboard**: `cd web && npm ci && npm run build`. Generated proto TS files require `buf generate --template buf.gen.web.yaml` first.

## Test Patterns

- We use `testify/assert` and `testify/require` for assertions.
- We favor **table-driven tests** for comprehensive coverage.
- We use `httptest` for testing HTTP servers/clients.
- We use Property-Based Testing (PBT) with `pgregory.net/rapid`.
- Packages with PBTs: `auth`, `proxy`, `webhook`, `controller`, `routing`, `argocd`, `api/v1alpha1`, `database`, `notifier`, `changeset`, `cli`, `topology`, `async`, `server/streaming`, `cache`, `session`.
- Currently passing **943 tests**.
- Config YAML tests **must** include `version: "1"` (strict decoding with `KnownFields(true)`).
- Config YAML tags use **snake_case** for most fields (`tag_template`, `header_key`, `baseline_namespace`) but **camelCase** for newer fields (`dependsOn`, `asyncRoutes`, `tagTemplate` is NOT valid — use `tag_template`).

## Conventions

- **Linting**: Code must pass `golangci-lint run` and `go vet`. `errcheck` requires all `fmt.Fprintf`/`fmt.Fprintln` return values to be checked.
- **Formatting**: `.go` files must use tabs (editorconfig enforced). Run `gofmt -w` before committing.
- **Commits**: Use conventional commit messages (`feat: ...`, `fix: ...`, `docs: ...`). Do **NOT** use `--no-verify` with git commit.
- **Hooks**: We use `lefthook` for pre-commit (go-vet, go-fmt, editorconfig, gitleaks) and pre-push (check-test-files, check-generated-stale, golangci-lint, go-test) hooks.
- **Test files**: Every `.go` source file **must** have a corresponding `_test.go` file (enforced by pre-push hook).
- **Idiomatic Go**: Write standard, idiomatic Go code with proper error handling.
- **Race safety**: CI runs `-race` tests. Use mutex-protected getters, not direct field access.

## Security Conventions

- Sentinel errors (e.g., `ErrEnvironmentNotFound`)
- Context timeouts on all external calls (e.g., 10s on git commands)
- Constant-time secret comparison for webhooks
- HeaderKey validation against RFC 7230
- SHA validation (hex-only regex) for commit status URLs
- Path traversal prevention in notifier API paths
- Label key/value validation using `k8s.io/apimachinery/pkg/util/validation`
- SQL injection prevention via regex-gated schema names (no parameterized DDL)
- Strict YAML unmarshaling (DisallowUnknownFields / `KnownFields(true)`)
- DeepCopy baseline before status mutations
- OIDC: CompositeProvider tries OIDCProvider → GitHubProvider → TokenReviewProvider in order
- Zitadel OIDC: roles returned as `map[string]interface{}` — keys are role names

## CI/CD

- **GitHub Actions**: Workflows in `.github/workflows/` — `ci.yml` (build+test, lint, file-hygiene, helm-lint, check-generated, test-guardrails), `e2e.yml` (dual-cluster k3d tests), `release.yml` (GoReleaser on `v*` tags).
- **GoReleaser**: Builds 4 binaries (controller, server, proxy, CLI) × 2 OS × 2 arch + slim variants. Dashboard built via `before.hooks` (buf generate + npm build). Docker images pushed to `ghcr.io/divergedev/diverge`.
- **Pre-push guards**: `check-test-files` (every .go needs _test.go), `check-generated-stale` (runs make generate + git diff), `golangci-lint`, `go-test`.

## Supported Protocols

- **HTTP/1.1 and HTTP/2** — via Gateway API HTTPRoute
- **gRPC** — via Gateway API GRPCRoute (`protocol: grpc` in service config)
- **WebSocket** — via HTTPRoute with `spec.serviceConfig.webSocket.enabled`, path matching, and configurable timeouts
