#!/usr/bin/env bash
# Setup script for Istio conformance E2E tests.
# Creates a Kind cluster with Istio service mesh + Gateway API.
set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-diverge-istio}"
ISTIO_VERSION="${ISTIO_VERSION:-1.24.3}"
GWAPI_VERSION="${GWAPI_VERSION:-v1.2.1}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "==> Creating Kind cluster '${CLUSTER_NAME}'..."
kind get clusters | grep -qx "${CLUSTER_NAME}" || \
  kind create cluster --name "${CLUSTER_NAME}" --config "${SCRIPT_DIR}/kind-config.yaml"

echo "==> Installing Gateway API CRDs ${GWAPI_VERSION}..."
kubectl apply --context "kind-${CLUSTER_NAME}" \
  -f "https://github.com/kubernetes-sigs/gateway-api/releases/download/${GWAPI_VERSION}/experimental-install.yaml"

echo "==> Installing istioctl ${ISTIO_VERSION}..."
ISTIO_BIN="/tmp/istioctl-${ISTIO_VERSION}"
if [ ! -f "${ISTIO_BIN}" ]; then
  OS=$(uname -s | tr '[:upper:]' '[:lower:]')
  ARCH=$(uname -m)
  if [ "${ARCH}" = "x86_64" ]; then
    ARCH="amd64"
  elif [ "${ARCH}" = "aarch64" ]; then
    ARCH="arm64"
  fi
  curl -fsSL "https://github.com/istio/istio/releases/download/${ISTIO_VERSION}/istioctl-${ISTIO_VERSION}-${OS}-${ARCH}.tar.gz" -o /tmp/istioctl.tar.gz
  tar -xzf /tmp/istioctl.tar.gz -C /tmp istioctl
  mv /tmp/istioctl "${ISTIO_BIN}"
  chmod +x "${ISTIO_BIN}"
fi

echo "==> Installing Istio Ambient mesh (istiod, istio-cni, ztunnel)..."
"${ISTIO_BIN}" install --set profile=ambient -y --context "kind-${CLUSTER_NAME}"
kubectl -n istio-system rollout status deployment/istiod --timeout=120s --context "kind-${CLUSTER_NAME}"
kubectl -n istio-system rollout status daemonset/istio-cni-node --timeout=120s --context "kind-${CLUSTER_NAME}"
kubectl -n istio-system rollout status daemonset/ztunnel --timeout=120s --context "kind-${CLUSTER_NAME}"

echo "==> Enrolling default namespace in Istio Ambient dataplane..."
LEGACY_INJECTION="$(kubectl get namespace default -o jsonpath='{.metadata.labels.istio-injection}' --context "kind-${CLUSTER_NAME}" 2>/dev/null || true)"
SIDECAR_PODS="$(kubectl get pods -n default -o jsonpath='{range .items[*]}{.metadata.annotations.sidecar\.istio\.io/status}{end}' --context "kind-${CLUSTER_NAME}" 2>/dev/null || true)"
kubectl label namespace default istio-injection- istio.io/dataplane-mode=ambient --context "kind-${CLUSTER_NAME}" --overwrite
if [ "${LEGACY_INJECTION}" = "enabled" ] || [ -n "${SIDECAR_PODS}" ]; then
  echo "==> Recreating existing default-namespace workloads to remove legacy sidecar proxies..."
  if [ -n "$(kubectl get deployment -n default -o name --context "kind-${CLUSTER_NAME}" 2>/dev/null)" ]; then
    kubectl rollout restart deployment -n default --context "kind-${CLUSTER_NAME}"
    kubectl rollout status deployment -n default --timeout=120s --context "kind-${CLUSTER_NAME}"
  fi
  if [ -n "$(kubectl get statefulset -n default -o name --context "kind-${CLUSTER_NAME}" 2>/dev/null)" ]; then
    kubectl rollout restart statefulset -n default --context "kind-${CLUSTER_NAME}"
    kubectl rollout status statefulset -n default --timeout=120s --context "kind-${CLUSTER_NAME}"
  fi
fi

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
  --set "routingProvider=gateway\,istio" \
  --set deploy.provider=direct \
  --set deploy.manifestSourceType=serviceconfig \
  --set sandbox.provider=noop \
  --kube-context "kind-${CLUSTER_NAME}"
kubectl -n diverge-system wait --for=condition=available \
  deployment/diverge-controller-manager --timeout=120s \
  --context "kind-${CLUSTER_NAME}" || \
  (kubectl -n diverge-system describe pods --context "kind-${CLUSTER_NAME}" && \
    kubectl -n diverge-system logs -l control-plane=controller-manager --all-containers --context "kind-${CLUSTER_NAME}" && exit 1)

echo "==> Istio E2E setup complete!"
