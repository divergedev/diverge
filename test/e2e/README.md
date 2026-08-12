# E2E Tests

End-to-end tests require a running Kubernetes cluster (k3d recommended).

## Prerequisites

- [Nix](https://nixos.org/download.html) (the repo's `flake.nix` provides all tools)
- Docker

## Run

All commands run from the **repository root** inside `nix develop`:

```bash
nix develop -c bash -c '
  cd demo/bank-demo
  ./scripts/setup.sh
  ./scripts/verify.sh
  ./scripts/setup.sh teardown
'
```

## What's tested

- Cluster creation and controller deployment
- Baseline service deployment
- Preview environment creation via Environment CR
- Header-based routing (baseline vs preview)
- Database schema isolation (PostgreSQL)
- Cleanup and resource deletion
