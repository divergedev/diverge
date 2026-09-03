# Istio Ambient Setup Guide for Diverge

> **Scope**: the `istio` routing provider writes an `AuthorizationPolicy` that
> restricts who may reach an intercepted service. It does **not** route by
> header, and `GetExternalURL` returns an empty string for it. Header-based
> preview routing — the mechanism that makes a preview a preview — requires the
> `gateway` or `composite` provider and Gateway API, whichever mesh is in use.
> Use `istio` alongside those for access control, not instead of them.

## Prerequisites
- Kubernetes 1.28+
- Istio 1.23+ with Ambient profile
- Diverge controller deployed

## Installation

### 1. Install Istio with Ambient Profile
```bash
istioctl install --set profile=ambient --skip-confirmation
```

### 2. Enable Ambient Mode on Preview Namespaces
You must configure your `Environment` to set the `istio.io/dataplane-mode: ambient` label on the target namespace:
```yaml
spec:
  namespaceLabels:
    istio.io/dataplane-mode: ambient
```
### 3. Configure DevIP for AuthorizationPolicy
Set the Tailscale IP in your Environment spec:
```yaml
apiVersion: diverge.io/v1alpha1
kind: Environment
metadata:
  name: my-preview
spec:
  routing:
    devIP: "100.64.x.x"  # Your Tailscale IP
```

### 4. Network Topology Requirements

> ⚠️ **IP Preservation**: The AuthorizationPolicy uses `ipBlocks` to identify
> developer traffic. This requires source IP preservation:
> - Set `externalTrafficPolicy: Local` on your LoadBalancer Service
> - Or use PROXY protocol
> - Configure `meshConfig.defaultConfig.gatewayTopology.numTrustedProxies`

### 5. Waypoint Proxies
For L7 traffic management (HTTP method matching, request transformation):
```bash
istioctl waypoint apply -n <preview-namespace>
```

Diverge's AuthorizationPolicy itself uses pure L4 rules, so it does not need a
waypoint. Header-based preview routing does: in ambient mode, L7 matching
happens at a waypoint, and ztunnel alone will not do it. If previews are
selected by header, install a waypoint for the preview namespace.

### 6. Reaching Your Machine

The `devIP` above assumes the cluster can open a connection *to* the developer
— a tailnet the nodes have joined, plus source IP preservation as described in
step 4. Where nodes are not tailnet members (most managed clusters, including
GKE), that will not work. Use the ConnectRPC tunnel instead, which dials
outward from your machine:

```bash
diverge dev --service <name>
```

The tunnel needs no `devIP`, no source IP preservation, and no inbound path to
your machine.

## Troubleshooting
- **503 errors**: Check that the ztunnel pods are running
- **Auth denied**: Verify DevIP matches your Tailscale IP (`tailscale ip -4`)
- **mTLS failures**: Ensure both namespaces are enrolled in ambient mesh
