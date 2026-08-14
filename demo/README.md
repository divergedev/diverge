# Diverge Demo

A fully reproducible local demo showcasing Diverge's preview environment platform.

## Prerequisites
- Docker
- k3d
- kubectl
- helm (optional)

## Quick Start
```bash
make demo
```

## What It Shows
1. **Gateway API Routing**: Header-based traffic splitting via HTTPRoute
2. **GAMMA Mesh Routing**: East-west service mesh routing without sidecars
3. **KNative Scale-to-Zero**: Preview environments that scale down when idle
4. **Database Isolation**: Per-preview schema isolation via search_path
5. **Collision Detection**: Two developers on the same service
6. **Dead Man's Switch**: Automatic cleanup on developer disconnect
7. **Edge Header Stripping**: Public ingress strips x-diverge-env
8. **AsyncRouter**: Composite sync+async routing
