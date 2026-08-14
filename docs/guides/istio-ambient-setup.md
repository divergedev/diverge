# Istio Ambient Setup Guide for Diverge

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

### 5. Waypoint Proxies (Optional)
For L7 traffic management (HTTP method matching, request transformation):
```bash
istioctl waypoint apply -n <preview-namespace>
```

Note: Diverge's AuthorizationPolicy uses pure L4 rules and does NOT
require Waypoint proxies for basic functionality.

## Troubleshooting
- **503 errors**: Check that the ztunnel pods are running
- **Auth denied**: Verify DevIP matches your Tailscale IP (`tailscale ip -4`)
- **mTLS failures**: Ensure both namespaces are enrolled in ambient mesh
