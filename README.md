# Diverge

![Diverge Logo](#)

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
See [CONTRIBUTING.md](#) for details.

## License
Apache 2.0
