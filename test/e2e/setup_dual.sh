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
echo "Deploying echo-server on management cluster (for single-cluster controller compatibility)..."
kubectl --context k3d-diverge-e2e-mgmt create deployment echo-server --image=ealen/echo-server:0.9.2 --port=80 || true
kubectl --context k3d-diverge-e2e-mgmt expose deployment echo-server --port=80 --target-port=80 || true

echo "Installing Envoy Gateway on management cluster..."
helm install eg oci://docker.io/envoyproxy/gateway-helm --version v1.2.6 -n envoy-gateway-system --create-namespace --kube-context k3d-diverge-e2e-mgmt --wait

echo "Creating Gateway resource..."
cat <<EOF | kubectl --context k3d-diverge-e2e-mgmt apply -f -
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: diverge-gateway
  namespace: default
spec:
  gatewayClassName: eg
  listeners:
  - name: http
    protocol: HTTP
    port: 80
EOF

echo "Waiting for Gateway to be ready..."
kubectl --context k3d-diverge-e2e-mgmt wait --for=condition=programmed gateway/diverge-gateway -n default --timeout=2m || true

echo "Building the controller..."
go build -o /tmp/diverge-controller ./cmd/controller/

echo "Starting the controller on management cluster..."
k3d kubeconfig get diverge-e2e-mgmt > /tmp/mgmt-kubeconfig
KUBECONFIG=/tmp/mgmt-kubeconfig /tmp/diverge-controller --deploy-provider=direct > /tmp/diverge-controller.log 2>&1 &
echo $! > /tmp/diverge-controller.pid

echo "Dual-cluster E2E setup complete."
