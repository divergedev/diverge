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

> [!TIP]
> Pass `--hide-environment-list` (`--hide-environment-list=true`) to `diverge-proxy` in multi-tenant or public-facing environments to suppress the list of active preview environments on the 404 landing page.

---

## 3. Proxy Flags & Routing Annotations

### `diverge-proxy` & `activator-proxy` Flags
| Binary | Flag | Default | Description |
| :--- | :--- | :--- | :--- |
| `diverge-proxy` | `--hide-environment-list` | `false` | Hide the list of active preview environments on the 404 landing page. |
| `activator-proxy` | `--wake-splash` | `false` | Serve a `202 Accepted` auto-refreshing HTML splash page (polling `GET /-/wakeup-status`) while waking scaled-to-zero pods. Can also be enabled per request via `X-Diverge-Wake-Splash: true`. |

### Environment & AgentTask Routing Annotations
| Resource | Annotation | Description |
| :--- | :--- | :--- |
| `Environment` | `divergedev.com/hibernate-after` | Idle duration (e.g., `"30m"`) after which the environment scales its preview Deployment down to `0` replicas (`Phase: Hibernating`). |
| `Environment` | `divergedev.com/last-traffic-at` | RFC 3339 timestamp of the last observed request (used with `divergedev.com/hibernate-after`). |
| `Environment` | `divergedev.com/ambient-mesh` | When `"true"`, automatically labels the preview namespace (`spec.deploy.namespace: create`) with `istio.io/dataplane-mode: ambient`. |
| `Environment` | `divergedev.com/waypoint` | Enrolls the preview namespace in the specified Istio Ambient Waypoint proxy (`istio.io/use-waypoint: <value>`). |
| `Environment` | `divergedev.com/gateway-namespace` | Overrides the namespace of the target `Gateway` in `HTTPRoute` / `GRPCRoute` `parentRefs` when the Ingress Gateway runs in a dedicated namespace (e.g., `envoy-gateway-system` or `istio-ingress`). |
| `AgentTask` | `divergedev.com/preview-enabled` | `"true"` / `"false"` — Controls automatic provisioning of a linked preview `Environment` (`agent-preview-<taskName>`). |
| `AgentTask` | `divergedev.com/preview-service` | Target service name for the `AgentTask`'s linked preview environment. |
| `AgentTask` | `divergedev.com/preview-image` | Container image for the linked preview environment (defaults to `nginx:alpine` on port `80`). |
| `AgentTask` | `divergedev.com/preview-branch` | Branch override for the linked preview environment. |
| `AgentTask` | `divergedev.com/preview-environment` | Output annotation written by the controller with the linked `Environment` name. |
| `AgentTask` | `divergedev.com/preview-url` | Output annotation written by the controller with the live preview URL once ready. |

---

## 4. Cross-Namespace `ReferenceGrant` & GAMMA East-West Mesh Routing

- **Automatic `ReferenceGrant` (`spec.deploy.namespace: create`)**: When an `Environment` deploys its preview workload into an isolated namespace (`env.PreviewNamespace()`) that differs from the route namespace, `GatewayRouter` automatically reconciles a `gateway.networking.k8s.io/v1beta1` `ReferenceGrant` (`diverge-<env>-grant`) in `env.PreviewNamespace()` granting route-namespace `HTTPRoute` and `GRPCRoute` access to each changed preview `Service` (`<env>-<service>`).
- **GAMMA East-West Routing (`--enable-gamma`)**: Pass `--enable-gamma` to `diverge-controller` so `PreviewGroupReconciler` configures child `Environment`s' `spec.serviceConfig.serviceName` with the baseline service name; `GatewayRouter` creates the GAMMA mesh `HTTPRoute` from `spec.serviceConfig.serviceName` (while `EnvironmentReconciler` does not take `--enable-gamma` and uses `spec.serviceConfig.serviceName` when set directly on the `Environment`).


