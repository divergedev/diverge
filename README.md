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
*   **MR-Triggered Lifecycle**: Environments spin up when a Merge Request opens and tear down upon merge/close.
*   **Argo CD Integration**: Direct `Application` CR creation for GitOps deployment sync.

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
make install
make run
```

Currently, the project contains **117 tests** utilizing table-driven tests and `testify/assert`.

## Roadmap

- [ ] **WebSocket Support** (#6) — Full WebSocket proxying for real-time preview environments
- [ ] **Controller EnvTest + E2E** (#9) — Comprehensive controller integration tests with envtest
- [ ] **Environment Proto + ConnectRPC API** (#12) — gRPC/ConnectRPC API server for environment management
- [ ] **CI Actions Node 22 Bump** (#25) — Migrate GitHub Actions to Node 22 runtime
- [ ] **Godoc Coverage** (#26) — Complete documentation for all exported functions

## License
Apache 2.0
