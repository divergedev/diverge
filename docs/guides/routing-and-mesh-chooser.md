# Service Mesh & Gateway API Chooser

Diverge supports pluggable routing providers via `--routing-provider` (`routingProvider` in Helm). Use this matrix to choose the best routing backend for your cluster.

---

## 1. Comparison Matrix

| Feature | `gateway-api` (Envoy Gateway / Cilium / Istio GW) | `istio` (Sidecar / Ambient) | `cilium` (eBPF L7) | `linkerd` (HTTPRoute) | `diverge-proxy` |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **Helm Value** | `routingProvider: gateway-api` | `routingProvider: istio` | `routingProvider: cilium` | `routingProvider: linkerd` | `proxy.enabled: true` |
| **HTTP Header Routing** | Yes (`HTTPRoute`) | Yes (`HTTPRoute` + `AuthorizationPolicy`) | Yes (`HTTPRoute` / `CiliumEnvoyConfig`) | Yes (`HTTPRoute`) | Yes (Reverse Proxy) |
| **gRPC / ConnectRPC Routing** | Yes (`GRPCRoute`) | Yes (`GRPCRoute`) | Yes (`GRPCRoute`) | Yes (`GRPCRoute`) | HTTP/1.1 + h2c |
| **East-West (Pod-to-Pod) Header Routing** | Via Mesh GAMMA | Yes | Yes | Yes | Via Proxy Service URL |
| **Zero-Trust mTLS Isolation** | No | Yes (Ambient / Sidecar) | Yes (WireGuard / Spire) | Yes | No |
| **Scale-to-Zero Compatible** | Yes | Yes | Yes | Yes | Yes |

---

## 2. Recommended Configurations

### Default: Envoy Gateway (`gateway-api`)
Best for most Kubernetes clusters that want standard Kubernetes Gateway API (`HTTPRoute` + `GRPCRoute`) without a full service mesh:

```yaml
routingProvider: gateway-api
gateway:
  name: diverge-gateway
  namespace: default
  className: eg
```

### Zero-Trust Multi-Tenant: Istio Ambient Mesh (`gateway-api,istio`)
Combine `gateway-api` and `istio` so Diverge generates both Gateway API `HTTPRoute`s and Istio `AuthorizationPolicy` rules while labeling preview namespaces with `istio.io/dataplane-mode: ambient`:

```yaml
routingProvider: "gateway-api,istio"
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
