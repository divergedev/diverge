<p align="center">
  <img src="docs/logo.jpg" alt="Diverge" width="200">
</p>

<h1 align="center">Diverge</h1>
<p align="center"><em>Branch your infrastructure, not just your code.</em></p>

<p align="center">
  <a href="https://github.com/divergedev/diverge/actions/workflows/ci.yml"><img src="https://github.com/divergedev/diverge/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/divergedev/diverge/blob/main/LICENSE"><img src="https://img.shields.io/badge/License-Apache%202.0-blue.svg" alt="License"></a>
  <a href="https://github.com/divergedev/diverge"><img src="https://img.shields.io/badge/Go-1.26-00ADD8.svg" alt="Go Version"></a>
</p>

Environment-as-a-service engine for Kubernetes. Diverge creates ephemeral preview environments triggered by merge request events, with delta deployment (only deploy changed services), configurable database provisioning, and Istio-based header routing.

Documentation: [https://divergedev.com](https://divergedev.com)

## Key Features
*   **Delta Deployment**: Only deploy what changed, falling back to a baseline for unmodified services.
*   **Header-Based Routing**: Leverages Istio and the Diverge Proxy to route traffic seamlessly using HTTP headers.
*   **Configurable DB Modes**: Options for shared, schema, snapshot, or fresh databases for your environments.
*   **Schema-per-Environment**: SQL-based schema provisioning with regex-validated naming and injection prevention.
*   **MR-Triggered Lifecycle**: Environments spin up when a Merge Request opens and tear down upon merge/close.
*   **Merge Gating**: GitLab/GitHub commit status checks (`diverge/preview`) block merges until environments are healthy.
*   **Argo CD Integration**: Direct `Application` CR creation for GitOps deployment sync, supporting both Helm charts and Kustomize overlays.
*   **Namespace Labels**: Custom labels on preview namespaces (e.g., `istio.io/dataplane-mode: ambient` for zero-trust mTLS).
*   **Security Hardened**: Webhook secret constant-time comparison, RFC 7230 header validation, SHA validation, path traversal prevention, strict YAML unmarshaling.
*   **Finalizer-Based Lifecycle**: Kubernetes finalizers ensure clean teardown of all resources (routing, database, ArgoCD apps) even during force-deletes.
*   **TTL Auto-Expiry**: Automatic environment cleanup after configurable TTL with requeue-based expiry.
*   **Multi-SCM Notifiers**: GitLab MR comments and GitHub PR comments with status updates.
*   **Proto Foundation**: Protobuf-defined domain types with ConnectRPC service definition for future API server.

## Security
Diverge takes security seriously. The platform features strict CRD OpenAPI validation, context timeouts on all external calls, and prevention mechanisms for shell/markdown injection in templates. The controller uses RBAC-scoped clients to ensure it only has the permissions it needs. Webhook interactions are secured using constant-time comparisons for secrets and RFC 7230-compliant header validation.

## Architecture

Diverge consists of 3 main components compiled into a single consolidated Docker image (`ghcr.io/divergedev/diverge:latest`), released via `goreleaser`:

1.  **Controller (`diverge-controller`)**: The Kubernetes operator that watches for `Environment` Custom Resources (CRs), reconciles them, and provisions the necessary resources (like Argo CD `Application` CRs, databases, etc.).
2.  **Proxy (`diverge-proxy`)**: A reverse proxy that helps facilitate header-based routing to preview environments.
3.  **CLI (`diverge`)**: A powerful CLI to interact with Diverge environments directly from your terminal.

## CLI Commands

The `diverge` CLI helps you manage environments efficiently:
*   `diverge init` - Initialize a `.diverge.yaml` config
*   `diverge create` - Create a new environment
*   `diverge list` - List active environments
*   `diverge status` - Check the status of an environment
*   `diverge open` - Open the preview URL in your browser
*   `diverge logs` - Stream logs for a preview environment
*   `diverge validate` - Validate your `.diverge.yaml`
*   `diverge delete` - Delete an environment
*   `diverge version` - Show CLI version

## Installation

The easiest way to install Diverge is via Helm:

```bash
helm repo add diverge https://charts.divergedev.io
helm repo update
helm install diverge diverge/diverge --namespace diverge-system --create-namespace
```

## Development

Diverge requires Go 1.26+. The project uses Nix to manage development dependencies.

```bash
# Enter the Nix development shell
nix develop

# Apply CRDs and run locally
nix develop -c make install
nix develop -c make run
```

Currently, the project contains **147 tests** utilizing table-driven tests, `testify/assert`, and Property-Based Testing (PBT) using the Hegel framework (`hegel.dev/go/hegel`).

## Roadmap

- [x] **CI Actions Node 22 Bump** (#25) — Migrate GitHub Actions to Node 22 runtime
- [x] **Godoc Coverage** (#26) — Complete documentation for all exported functions
- [x] **GitLab/GitHub Commit Statuses** — Merge gating via `diverge/preview` commit status checks
- [x] **Schema-per-Environment** — SQL-based schema provisioning with SchemaProvider
- [x] **Namespace Labels** — Custom labels on preview namespaces (Istio Ambient mTLS)
- [x] **Proto Foundation** — Protobuf domain types + ConnectRPC service definition
- [ ] **WebSocket Support** (#6) — Full WebSocket proxying for real-time preview environments
- [ ] **Controller EnvTest + E2E** (#9) — Comprehensive controller integration tests with envtest
- [ ] **ConnectRPC API Server** (#12) — gRPC/ConnectRPC API server for environment management

## License
Apache 2.0
