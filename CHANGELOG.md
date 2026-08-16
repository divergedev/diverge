# Changelog

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
