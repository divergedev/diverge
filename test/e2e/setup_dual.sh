#!/usr/bin/env bash
set -euo pipefail

echo "Creating management cluster..."
k3d cluster create diverge-e2e-mgmt --no-lb --k3s-arg "--disable=traefik@server:0" --wait

echo "Creating production cluster..."
k3d cluster create diverge-e2e-prod --no-lb --k3s-arg "--disable=traefik@server:0" --wait

echo "Installing CRDs on management cluster..."
kubectl --context k3d-diverge-e2e-mgmt apply -f config/crd/bases/

echo "Installing Gateway API CRDs..."
kubectl --context k3d-diverge-e2e-mgmt apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.2.1/standard-install.yaml
kubectl --context k3d-diverge-e2e-prod apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.2.1/standard-install.yaml

echo "Deploying echo-server on production cluster..."
kubectl --context k3d-diverge-e2e-prod create deployment echo-server --image=ealen/echo-server:0.9.2 --port=80
kubectl --context k3d-diverge-e2e-prod expose deployment echo-server --port=80 --target-port=80

echo "Dual-cluster E2E setup complete."
