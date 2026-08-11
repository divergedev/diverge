# E2E Tests

End-to-end tests require a running Kubernetes cluster (k3d recommended).

## Prerequisites

- k3d
- kubectl
- helm
- Docker

## Run

```bash
# Use the bank demo as the e2e test suite
cd ../demo/bank-demo
./scripts/setup.sh
./scripts/verify.sh
./scripts/setup.sh teardown
```

## What's tested

- Cluster creation and controller deployment
- Baseline service deployment
- Preview environment creation via Environment CR
- Header-based routing (baseline vs preview)
- Database schema isolation (PostgreSQL)
- Cleanup and resource deletion
