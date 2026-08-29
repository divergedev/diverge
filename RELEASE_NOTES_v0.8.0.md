# Diverge v0.8.0

Diverge v0.8.0 introduces composable preview environments powered by intelligent service topology discovery, request route simulation, and precise changed-service detection. With this release, Diverge can model runtime dependencies across your microservices using static configuration, Kubernetes Gateway API, or live Prometheus metrics from Istio, Linkerd, and Cilium service meshes. Developers can now simulate request routing with `diverge route`, visualize topologies in Mermaid, DOT, or JSON format, and instantly detect which services are affected by branch changes with `diverge diff`.

---

## 🚀 What's New

### 1. Composable Environments & Service Topology ([#221](https://github.com/divergedev/diverge/pull/221))

Preview environments can now compose dynamically across multi-service architectures with topology-aware ingress resolution:
- **Service Graph Engine**: In-memory directed graph engine (`pkg/topology`) to track nodes, edges, protocols, and ingress entrypoints.
- **Topology Discovery**:
  - **Gateway API Discovery**: Discovers upstream/downstream service relationships and routing rules directly from Gateway API HTTPRoutes.
  - **Static Config Discovery**: Defines service dependencies and entrypoint gateways directly in `.diverge.yaml` via `dependsOn` and `entrypoint`.
- **Ingress Path Resolution**: Calculates all possible ingress paths from entrypoints to preview services to ensure accurate edge routing and header propagation.
- **CLI Commands**:
  - `diverge graph show`: Inspect and display discovered topology trees or filter by service and gateway.
  - `diverge graph validate`: Validate service graph integrity, detect orphaned services, and verify reachability from entrypoints.

### 2. Route Simulation & Output Formatters ([#222](https://github.com/divergedev/diverge/pull/222))

Trace, simulate, and visualize how requests flow through ephemeral preview environments:
- **Route Simulation (`diverge route <service>`)**: Simulates request paths from ingress entrypoints to target services, verifying header propagation hops and route viability before deployment.
- **Output Formatters**: Export topology and route simulations with `--output=mermaid`, `--output=dot`, or `--output=json` for documentation, CI pipelines, and visual diagrams.
- **Prometheus Service Mesh Discovery**: `PrometheusDiscoverer` automatically detects service-to-service call graphs from live service mesh telemetry, supporting **Istio**, **Linkerd**, and **Cilium** metrics out-of-the-box.
- **Background Topology Cache**: In-memory topology cache with stale-while-revalidate (`ttl` and `staleTTL`) ensuring instant CLI responses while refreshing topology in the background.
- **Kubernetes Prometheus Discovery**: Automatically discovers in-cluster Prometheus instances and services.
- **Header Propagation Documentation & SDKs**: Comprehensive [Header Propagation Guide](docs/header-propagation.md) with architectural sequence diagrams and middleware recipes across Go, Node.js, Python, and Java.

### 3. Changed-Service Detection ([#223](https://github.com/divergedev/diverge/pull/223))

Accurately target preview deployments only to the services modified in your pull request or branch:
- **`diverge diff` Command**: Compares current workspace or branch against a base ref (e.g., `main`), mapping modified files to services defined in `.diverge.yaml`.
- **Real `GitChangeDetector`**: Production git diff engine with a 10s timeout, replacing the previous stub implementation.
- **Path-Based Service Matching**: Fast glob and prefix matching with support for multi-path services and root fallbacks.
- **Upstream Impact Analysis**: Shows downstream changed services alongside potentially affected upstream callers using the service topology graph.
- **CI/Bot Friendly**: Supports `--output=json` and `DetectChangesFromFiles` for headless CI/CD and GitHub Action integrations.
- **Property-Based Testing**: Validated with 5 Rapid property-based tests verifying subset constraints, deduplication, deterministic sorting, monotonicity, and empty-set behavior.

---

## ⚠️ Breaking Changes

**None**. Diverge v0.8.0 is fully backward compatible with existing `.diverge.yaml` configurations and `Environment` / `PreviewGroup` CRDs.

---

## 📦 Upgrade Guide

### 1. Upgrade Helm Chart
Upgrade the Diverge controller and proxy in your Kubernetes cluster:
```bash
helm repo update diverge
helm upgrade diverge diverge/diverge --namespace diverge-system
```

### 2. Update CLI
Upgrade the `diverge` CLI on your local development machine:
```bash
# Via install script
curl -fsSL https://divergedev.com/install.sh | sh

# Or via Go
go install github.com/divergedev/diverge/cmd/diverge@v0.8.0
```

---

## 💻 Installation

### Install CLI via Script
```bash
curl -fsSL https://raw.githubusercontent.com/divergedev/diverge/v0.8.0/install.sh | sh
```

### Install CLI via Go
```bash
go install github.com/divergedev/diverge/cmd/diverge@v0.8.0
```

### Docker Pull
```bash
docker pull ghcr.io/divergedev/diverge:v0.8.0
```

### Deploy to Kubernetes via Helm
```bash
helm repo add diverge https://divergedev.github.io/diverge
helm repo update
helm install diverge diverge/diverge --namespace diverge-system --create-namespace
```

---

## 📊 Release Stats

- **PRs Merged**: 4 PRs ([#221](https://github.com/divergedev/diverge/pull/221), [#222](https://github.com/divergedev/diverge/pull/222), [#223](https://github.com/divergedev/diverge/pull/223), [#224](https://github.com/divergedev/diverge/pull/224))
- **Lines Added**: +3,800+ lines of Go, YAML, and Markdown documentation
- **Tests Added**: 30+ new unit, integration, and property-based test cases

---

## 👥 Contributors

This release was brought to you by the Diverge team.

---

**Full Changelog**: https://github.com/divergedev/diverge/compare/v0.7.0...v0.8.0
