#!/usr/bin/env bash
set -euo pipefail
k3d cluster delete diverge-e2e-mgmt 2>/dev/null || true
k3d cluster delete diverge-e2e-prod 2>/dev/null || true
echo "Dual-cluster E2E teardown complete."
