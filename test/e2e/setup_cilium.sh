#!/usr/bin/env bash
# Setup script for Cilium Gateway API conformance E2E tests.
# Creates a Kind cluster with Cilium as CNI + Gateway API provider.
set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-diverge-cilium}"
CILIUM_VERSION="${CILIUM_VERSION:-1.17.3}"
GWAPI_VERSION="${GWAPI_VERSION:-v1.2.1}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "==> Creating Kind cluster '${CLUSTER_NAME}' (no default CNI)..."
kind get clusters | grep -q "${CLUSTER_NAME}" || \
  kind create cluster --name "${CLUSTER_NAME}" --config "${SCRIPT_DIR}/kind-cilium.yaml"

echo "==> Installing Gateway API CRDs ${GWAPI_VERSION}..."
kubectl apply --context "kind-${CLUSTER_NAME}" \
  -f "https://github.com/kubernetes-sigs/gateway-api/releases/download/${GWAPI_VERSION}/standard-install.yaml"

echo "==> Installing Cilium ${CILIUM_VERSION} with Gateway API support..."
helm repo add cilium https://helm.cilium.io/ 2>/dev/null || true
helm repo update cilium
helm upgrade --install cilium cilium/cilium \
  --version "${CILIUM_VERSION}" \
  --namespace kube-system \
  --set kubeProxyReplacement=true \
  --set gatewayAPI.enabled=true \
  --set image.pullPolicy=IfNotPresent \
  --set operator.replicas=1 \
  --kube-context "kind-${CLUSTER_NAME}" \
  --wait --timeout 120s

echo "==> Waiting for Cilium agent to be ready..."
kubectl -n kube-system rollout status daemonset/cilium \
  --context "kind-${CLUSTER_NAME}" --timeout=120s

echo "==> Creating diverge-gateway Gateway..."
kubectl apply --context "kind-${CLUSTER_NAME}" -f - <<'EOF'
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: diverge-gateway
  namespace: default
spec:
  gatewayClassName: cilium
  listeners:
    - name: http
      protocol: HTTP
      port: 80
      allowedRoutes:
        namespaces:
          from: All
EOF

echo "==> Waiting for Gateway to be accepted..."
kubectl wait gateway/diverge-gateway --for=condition=Accepted \
  --context "kind-${CLUSTER_NAME}" --timeout=120s || true

echo "==> Building and loading controller image..."
make docker-build
kind load docker-image divergedev/diverge:latest --name "${CLUSTER_NAME}"

echo "==> Deploying Diverge CRDs and controller..."
kubectl apply -f config/crd/bases/ --context "kind-${CLUSTER_NAME}"
kubectl apply -k config/default --context "kind-${CLUSTER_NAME}" || true
kubectl -n diverge-system wait --for=condition=available \
  deployment/diverge-controller --timeout=120s \
  --context "kind-${CLUSTER_NAME}" || true

echo "==> Cilium E2E setup complete!"
