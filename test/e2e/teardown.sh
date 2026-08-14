#!/usr/bin/env bash
set -euo pipefail

echo "Tearing down e2e environment in cluster k3d-oneazra-dev..."

# 1. Switch context
kubectl config use-context k3d-oneazra-dev

# 2. Delete test namespace
echo "Deleting test namespace diverge-e2e-test..."
kubectl delete namespace diverge-e2e-test --ignore-not-found=true

echo "E2E Teardown complete!"
