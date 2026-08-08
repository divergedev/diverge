
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

## Key Features
*   **Delta Deployment**: Only deploy what changed, falling back to a baseline for unmodified services.
*   **Header-Based Routing**: Leverages Istio to route traffic seamlessly using HTTP headers.
*   **Configurable DB Modes**: Options for shared, schema, snapshot, or fresh databases for your environments.
*   **MR-Triggered Lifecycle**: Environments spin up when a Merge Request opens and tear down upon merge/close.
*   **Multi-Environment Support**: Supports preview, staging, demo, and QA environments.

## Quick Start
1. Apply the CRDs: `make install`
2. Run the operator locally: `make run`
3. Apply a sample environment: `kubectl apply -f config/samples/environment_preview.yaml`

## Architecture Overview
The Diverge operator watches for `Environment` Custom Resources (CRs). A webhook listens to GitLab events (MR created/updated) and automatically generates these CRs. The operator reconciles these CRs, detecting changes, provisioning databases, and configuring Istio routing rules.

## Contributing
See [CONTRIBUTING.md](CONTRIBUTING.md) for details.

## License
Apache 2.0
