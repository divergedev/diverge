# Diverge Bank Demo — Multi-Repo Preview Environments

> Deploy a preview of ONE microservice. The mesh routes tagged traffic to it;
> everything else hits the baseline. Zero wasted resources.

## Architecture

```
┌──────────────┐    ┌──────────────┐    ┌──────────────────────┐
│  web-app     │───▶│  gateway     │───▶│ payments-api         │
│  (baseline)  │    │  (baseline)  │    │ ┌──────────────────┐ │
└──────────────┘    └──────────────┘    │ │ baseline (main)  │ │
                                        │ ├──────────────────┤ │
                                        │ │ PREVIEW (MR-42)  │◀── x-preview-id: 42
                                        │ └──────────────────┘ │
                                        └──────────┬───────────┘
                                                   │
                                        ┌──────────▼───────────┐
                                        │ accounts-api         │
                                        │ (baseline)           │
                                        └──────────────────────┘
```

Only the **payments-api** has a preview pod. The Gateway API HTTPRoute routes
traffic with `x-preview-id: 42` to the preview; everything else flows through
the baseline services.

## Prerequisites

- [k3d](https://k3d.io) (lightweight K8s in Docker)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [helm](https://helm.sh/docs/intro/install/)
- [Docker](https://docs.docker.com/get-docker/)
- [jq](https://jqlang.github.io/jq/) (optional, for pretty output)

## Quick Start

```bash
# 1. Set up cluster + baseline services (~2 minutes)
./scripts/setup.sh

# 2. Test baseline
curl -s http://localhost:8080/api/payments | jq
# → {"service": "payments-api", "version": "baseline", ...}

# 3. Simulate an MR (creates preview pod + HTTPRoute)
./scripts/simulate-preview.sh 42

# 4. Test preview routing
curl -s -H 'x-preview-id: 42' http://localhost:8080/api/payments | jq
# → {"service": "payments-api", "version": "preview-42", ...}

# 5. Compare side-by-side
curl -s http://localhost:8080/api/payments | jq '.version'
# → "baseline"
curl -s -H 'x-preview-id: 42' http://localhost:8080/api/payments | jq '.version'
# → "preview-42"

# 6. Cleanup
./scripts/cleanup-preview.sh 42

# 7. Teardown everything
./scripts/setup.sh teardown
```

## What's Happening

### Setup (`setup.sh`)
1. Creates a k3d cluster with port mapping (8080 → ingress)
2. Installs Gateway API CRDs + Envoy Gateway
3. Builds all 4 demo service images locally
4. Loads images into k3d (no registry needed)
5. Deploys baseline services (simulating your ArgoCD-managed dev environment)
6. Creates baseline HTTPRoutes

### Preview (`simulate-preview.sh`)
1. Builds a modified `payments-api` image (simulating a CI build from an MR)
2. Deploys a **preview pod** in the same namespace as baseline
3. Creates an HTTPRoute with header matching: `x-preview-id: 42` → preview pod

This is exactly what the Diverge controller does automatically when a webhook fires.

### Cleanup (`cleanup-preview.sh`)
Deletes preview resources by label selector (`diverge.io/preview-id`).
This is what the controller does when an MR is merged or closed.

## Demo Repos

These are independent GitHub repos, simulating a real polyrepo architecture:

| Repo | Service | Role |
|------|---------|------|
| [demo-web-app](https://github.com/divergedev/demo-web-app) | Frontend dashboard | Calls gateway |
| [demo-gateway](https://github.com/divergedev/demo-gateway) | API BFF | Routes to payments + accounts |
| [demo-payments-api](https://github.com/divergedev/demo-payments-api) | Payments service | Calls accounts-api |
| [demo-accounts-api](https://github.com/divergedev/demo-accounts-api) | Accounts service | Leaf service |

Each repo contains a `.diverge.yaml` that tells Diverge how to create preview pods.

## How It Maps to Production

| Demo | Production |
|------|-----------|
| k3d cluster | Your GKE/EKS dev cluster |
| Envoy Gateway | Istio Waypoint Proxy |
| `setup.sh` | ArgoCD manages baseline |
| `simulate-preview.sh` | Diverge controller (automated via webhook) |
| `cleanup-preview.sh` | Diverge controller (automated on MR merge) |
| Manual `curl -H` | OTel Baggage propagation across services |
