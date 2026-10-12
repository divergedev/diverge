# Service Mesh & Gateway API Chooser

Diverge supports pluggable routing providers via `--routing-provider` (`routingProvider` in Helm). Registered provider names are `gateway`, `istio`, `composite`, `noop`, and comma-separated combinations (such as `"gateway,istio"`). Use this matrix to choose the best routing backend for your cluster.

---

## 1. Comparison Matrix

| Feature | Gateway API (Envoy Gateway / Cilium / Linkerd) | Istio (Sidecar / Ambient) | Gateway API + Istio | `diverge-proxy` |
| :--- | :--- | :--- | :--- | :--- |
| **Helm Value** | `routingProvider: gateway` | `routingProvider: istio` | `routingProvider: "gateway,istio"` | `routingProvider: noop` + `proxy.enabled: true` |
| **HTTP Header Routing** | Yes (`HTTPRoute`) | Yes (`HTTPRoute` + `AuthorizationPolicy`) | Yes (`HTTPRoute` + `AuthorizationPolicy`) | Yes (Reverse Proxy) |
| **gRPC / ConnectRPC Routing** | Yes (`GRPCRoute`) | Yes (`GRPCRoute`) | Yes (`GRPCRoute`) | HTTP/1.1 + h2c |
| **East-West (Pod-to-Pod) Header Routing** | Via Mesh GAMMA (Cilium / Linkerd) | Yes | Yes | Via Proxy Service URL |
| **Zero-Trust mTLS Isolation** | Via Mesh (Cilium WireGuard / Linkerd) | Yes (Ambient / Sidecar) | Yes (Ambient / Sidecar) | No |
| **Scale-to-Zero Compatible** | Yes | Yes | Yes | Yes |

---

## 2. Recommended Configurations

### Default: Kubernetes Gateway API (`gateway`)
Best for clusters running Envoy Gateway, Cilium Gateway API, or Linkerd with standard Kubernetes Gateway API (`HTTPRoute` + `GRPCRoute`):

```yaml
routingProvider: gateway
gateway:
  name: diverge-gateway
  namespace: default
  className: eg
```

### Zero-Trust Multi-Tenant: Istio Ambient Mesh (`"gateway,istio"`)
Combine `gateway` and `istio` so Diverge generates both Gateway API `HTTPRoute`s and Istio `AuthorizationPolicy` rules while labeling preview namespaces with `istio.io/dataplane-mode: ambient`:

```yaml
routingProvider: "gateway,istio"
namespaceLabels:
  istio.io/dataplane-mode: ambient
```

### Lightweight No-Mesh Clusters: `diverge-proxy`
If your cluster does not have Gateway API CRDs installed, enable the built-in L7 header-aware reverse proxy:

```yaml
routingProvider: noop
proxy:
  enabled: true
  defaultBackend: http://production-gateway.default.svc.cluster.local:8080
```
