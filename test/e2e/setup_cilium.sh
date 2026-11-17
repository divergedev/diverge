#!/usr/bin/env bash
# Setup script for Cilium Gateway API conformance E2E tests.
# Creates a Kind cluster with Cilium as CNI + Gateway API provider.
set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-diverge-cilium}"
CILIUM_VERSION="${CILIUM_VERSION:-1.17.3}"
GWAPI_VERSION="${GWAPI_VERSION:-v1.2.1}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "==> Creating Kind cluster '${CLUSTER_NAME}' (no default CNI)..."
kind get clusters | grep -qx "${CLUSTER_NAME}" || \
  kind create cluster --name "${CLUSTER_NAME}" --config "${SCRIPT_DIR}/kind-cilium.yaml"

API_SERVER_IP="$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "${CLUSTER_NAME}-control-plane" | head -n1)"
echo "==> Kind control-plane API IP: ${API_SERVER_IP}"

echo "==> Installing Gateway API CRDs (experimental ${GWAPI_VERSION} for Cilium TLSRoute/GRPCRoute)..."
kubectl apply --context "kind-${CLUSTER_NAME}" \
  -f "https://github.com/kubernetes-sigs/gateway-api/releases/download/${GWAPI_VERSION}/experimental-install.yaml"

echo "==> Installing Cilium ${CILIUM_VERSION} with Gateway API support..."
helm repo add cilium https://helm.cilium.io/ 2>/dev/null || true
helm repo update cilium
helm upgrade --install cilium cilium/cilium \
  --version "${CILIUM_VERSION}" \
  --namespace kube-system \
  --set kubeProxyReplacement=true \
  --set "k8sServiceHost=${API_SERVER_IP}" \
  --set k8sServicePort=6443 \
  --set gatewayAPI.enabled=true \
  --set envoy.enabled=true \
  --set image.pullPolicy=IfNotPresent \
  --set operator.replicas=1 \
  --kube-context "kind-${CLUSTER_NAME}"

echo "==> Waiting for Cilium agent, Envoy, and operator to be ready..."
kubectl -n kube-system rollout status daemonset/cilium \
  --context "kind-${CLUSTER_NAME}" --timeout=300s || \
  (kubectl -n kube-system get pods -o wide --context "kind-${CLUSTER_NAME}" && \
    kubectl -n kube-system describe pods -l k8s-app=cilium --context "kind-${CLUSTER_NAME}" && \
    kubectl -n kube-system logs -l k8s-app=cilium --all-containers --tail=100 --context "kind-${CLUSTER_NAME}" && exit 1)
kubectl -n kube-system rollout status deployment/cilium-operator \
  --context "kind-${CLUSTER_NAME}" --timeout=180s
kubectl -n kube-system rollout status daemonset/cilium-envoy \
  --context "kind-${CLUSTER_NAME}" --timeout=180s

LB_POOL_CIDR="$(echo "${API_SERVER_IP}" | cut -d. -f1-2).255.200/29"
echo "==> Configuring CiliumLoadBalancerIPPool (${LB_POOL_CIDR}) and diverge-gateway..."
kubectl wait --for=condition=Established crd/ciliumloadbalancerippools.cilium.io \
  --context "kind-${CLUSTER_NAME}" --timeout=60s
kubectl apply --context "kind-${CLUSTER_NAME}" -f - <<EOF
apiVersion: "cilium.io/v2alpha1"
kind: CiliumLoadBalancerIPPool
metadata:
  name: diverge-e2e-pool
spec:
  blocks:
    - cidr: "${LB_POOL_CIDR}"
---
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

echo "==> Waiting for Gateway to be programmed..."
kubectl wait gateway/diverge-gateway --for=condition=Programmed \
  --context "kind-${CLUSTER_NAME}" --timeout=120s
GW_NODEPORT="$(kubectl -n default get svc cilium-gateway-diverge-gateway --context "kind-${CLUSTER_NAME}" -o jsonpath='{.spec.ports[0].nodePort}')"
echo "==> Cilium Gateway NodePort ready at http://${API_SERVER_IP}:${GW_NODEPORT}"

echo "==> Building and loading controller image..."
make docker-build
kind load docker-image divergedev/diverge:latest --name "${CLUSTER_NAME}"

echo "==> Deploying Diverge CRDs and controller..."
kubectl apply -f config/crd/bases/ --context "kind-${CLUSTER_NAME}"
kubectl create namespace diverge-system --dry-run=client -o yaml | kubectl apply --context "kind-${CLUSTER_NAME}" -f -
TLS_DIR="$(mktemp -d)"
cleanup() {
  local rc=$?
  rm -rf "${TLS_DIR}"
  exit "${rc}"
}
trap cleanup EXIT
openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout "${TLS_DIR}/tls.key" -out "${TLS_DIR}/tls.crt" \
  -days 1 -subj "/CN=diverge-webhook.diverge-system.svc" 2>/dev/null
kubectl -n diverge-system create secret tls diverge-webhook-tls \
  --cert="${TLS_DIR}/tls.crt" --key="${TLS_DIR}/tls.key" \
  --dry-run=client -o yaml | kubectl apply --context "kind-${CLUSTER_NAME}" -f -
rm -rf "${TLS_DIR}"

helm upgrade --install diverge charts/diverge --namespace diverge-system \
  --skip-crds \
  --set image.repository=divergedev/diverge \
  --set image.tag=latest \
  --set image.pullPolicy=Never \
  --set deploy.provider=direct \
  --set deploy.manifestSourceType=serviceconfig \
  --set sandbox.provider=noop \
  --kube-context "kind-${CLUSTER_NAME}"
kubectl -n diverge-system wait --for=condition=available \
  deployment/diverge-controller-manager --timeout=120s \
  --context "kind-${CLUSTER_NAME}" || \
  (kubectl -n diverge-system describe pods --context "kind-${CLUSTER_NAME}" && \
    kubectl -n diverge-system logs -l control-plane=controller-manager --all-containers --context "kind-${CLUSTER_NAME}" && exit 1)

echo "==> Cilium E2E setup complete!"
