# Diverge Demo

A fully reproducible local demo showcasing Diverge's preview environment platform.

## Prerequisites
- Docker
- k3d
- kubectl
- helm

## Quick Start
```bash
make demo          # Builds cluster, deploys controller + sample services (~2 min)
make demo-killer   # Run the headline demo
make demo-teardown # Destroy cluster
```

## What It Shows
1. **Gateway API Routing**: Header-based traffic splitting via HTTPRoute
2. **GAMMA Mesh Routing**: East-west service mesh routing without sidecars
3. **KNative Scale-to-Zero**: Preview environments that scale down when idle
4. **Database Isolation**: Per-preview schema isolation via search_path
5. **Collision Detection**: Two developers on the same service
6. **Auto-Cleanup on Disconnect**: Automatic cleanup when developer disconnects
7. **Edge Header Stripping**: Public ingress strips x-diverge-env
8. **AsyncRouter**: Composite sync+async routing
