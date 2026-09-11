# Changelog

## [v0.11.0] - 2026-09-11

### ⚠️ Critical Migration Notice: CRD API Group Transition to `divergedev.com`
The CRD API group, labels, annotations, and RBAC bindings have migrated from `diverge.io` to `divergedev.com` (e.g. `divergedev.com/v1alpha1`).
- Upgrading clusters must install the new CRD manifests: `charts/diverge/crds/divergedev.com_*.yaml`.
- Existing `Environment` and `PreviewGroup` custom resources under `diverge.io/v1alpha1` should be updated to `divergedev.com/v1alpha1`.

### 🚀 Highlights & New Features

#### 🚩 Enterprise Feature Flag Providers & OpenFeature Integration
- **Flagsmith Provider**: Full enterprise integration supporting identity and trait targeting (`pkg/features/flagsmith`).
- **Flipt Provider**: Dual-tier secret resolution and evaluation provider (`pkg/features/flipt`).
- **Unleash Provider**: Extensible Unleash provider implementation (`pkg/features/unleash`).
- **OpenFeature Hook**: Generic OpenFeature evaluation hook injecting Diverge preview context into flag evaluations (`pkg/sdk/openfeature`).
- **Dashboard Feature Flags Tab**: Dedicated Feature Flags view in the web dashboard for inspecting active provider state and evaluation rules per environment.

#### 🩺 Cluster Doctor & Preview Verification Suite
- **`diverge doctor` Diagnoser**: Automated health check CLI diagnosing cluster connectivity, CRD installation, DNS resolution, and ingress routes.
- **Preview Verification Suite**: Comprehensive automated test harness validating end-to-end routing integrity, header propagation, and container health across preview environments.

#### 💻 Unified VS Code & OpenCode Extension
- Released unified editor extension (`editors/vscode`) providing preview environment lifecycle controls, active session status bars, and direct terminal launching inside IDEs.

#### 👥 Multi-User Conflict Detection & Session Leasing
- Advanced session leasing and route conflict detection for `diverge dev` (Pro), preventing concurrent developers on shared clusters from overwriting routes.

#### 🗄️ Database Migration Isolation with Atlas
- Support for standalone Atlas migration jobs with production-grade local bundling, lifecycle gating, and CLI support.

#### 🌐 Advanced Routing, IPv6, and Gateway API Conformance
- Support for multi-provider routing composition, IPv6 CIDRs, and direct ingress gateway routing.
- Added comprehensive Kubernetes Gateway API conformance test suite.

#### 📦 Cross-Language Context Propagation SDKs
- Published context propagation and header injection SDKs across multiple languages:
  - Go: `pkg/sdk/http`, `pkg/sdk/grpc`, `pkg/sdk/connect`, `pkg/sdk/kafka`
  - Node.js: `@divergedev/sdk` in `pkg/sdk/node`
  - Python: `diverge-sdk` in `pkg/sdk/python`

#### 🔐 Dynamic TokenSource & OpenBao Integration
- Introduced dynamic `TokenSource` with automatic credential reloading, OpenBao support, and in-cluster `KubeTokenSource`.

### 🛠️ Bug Fixes & Hardening

- **HTTP/2 h2c Tunnel Transport (#281)**: Enabled plaintext h2c protocol negotiation on unencrypted server connections, resolving HTTP 505 errors when running behind ingress proxies or development clusters without TLS.
- **Unbounded Tunnel Stream Timeouts (#282)**: Removed `ReadTimeout` limitation on long-lived bidirectional reverse tunnel streams.
- **Tunnel Service Resolution on kube-dns (#283)**: Fixed `Endpoints` sweeping, label selectors, and transition handling for CoreDNS/kube-dns clusters.
- **Browser Authentication Redirects (#284)**: Redirects unauthenticated browser sessions to the login endpoint with strict URL sanitization to prevent open-redirect vulnerabilities, while preserving HTTP 401 for API clients.
- **CRD `local` Provider Support (#286)**: Added `local` to `EnvironmentSource.Provider` enum and made `Project` optional, enabling `diverge dev` to natively create valid `PreviewGroup` resources.
- **Graceful CLI Cleanup (#285)**: Ensures `diverge dev` terminates cleanly and removes local artifacts even if the cluster runs in a degraded or restricted mode.
- **Flaky Load Test Stabilization (#286)**: Stabilized baseline comparisons in CLI load test smoke tests against CI runner thread scheduling jitter.

---

## [v0.4.0] - 2026-08-16

### 🚀 ConnectRPC Server Foundation

Diverge now includes a stateless ConnectRPC API server, giving developers access to preview environments without needing direct Kubernetes credentials.

#### Highlights
- **ConnectRPC Server**: Full CRUD API for Environments and PreviewGroups over HTTP/2 + gRPC
- **OIDC Authentication**: Zitadel-compatible OIDC auth with K8s TokenReview fallback
- **Streaming**: Real-time watch events and multi-pod log streaming with 4-hour max duration
- **CLI Login**: `diverge login --server <url> --token <token>` with secure credential storage
- **Context Switching**: `diverge context` for managing multiple server connections
- **Dual-Mode Client**: CLI works with direct K8s access OR via ConnectRPC server
- **Helm Chart**: `server.enabled: true` deploys the API server alongside the controller

#### Security
- Auth interceptor covers both unary and streaming RPCs
- RBAC via SubjectAccessReview on all operations
- Error sanitization prevents K8s internal state leakage
- Atomic config file saves prevent credential corruption
- Callback server binds to localhost only

#### Infrastructure
- Property-based tests for CRD types using pgregory.net/rapid
- Bounded broadcaster with 64-event ring buffer
- Stream concurrency limits (max 100)
- CI: buf lint/breaking checks, generated file staleness detection
- Proto stability enforcement with field number locking

### Previous Releases
See [GitHub Releases](https://github.com/divergedev/diverge/releases) for v0.1.0–v0.3.0.
