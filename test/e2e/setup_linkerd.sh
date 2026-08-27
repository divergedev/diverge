#!/usr/bin/env bash
# Setup script for Linkerd GAMMA conformance E2E tests.
# Creates a Kind cluster with Linkerd service mesh + Gateway API.
set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-diverge-linkerd}"
LINKERD_VERSION="${LINKERD_VERSION:-stable-2.16.2}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "==> Creating Kind cluster '${CLUSTER_NAME}'..."
kind get clusters | grep -q "${CLUSTER_NAME}" || \
  kind create cluster --name "${CLUSTER_NAME}" --config "${SCRIPT_DIR}/kind-config.yaml"

echo "==> Installing Gateway API CRDs..."
kubectl apply --context "kind-${CLUSTER_NAME}" \
  -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.2.1/standard-install.yaml

echo "==> Installing Linkerd CLI ${LINKERD_VERSION}..."
LINKERD_BIN="/tmp/linkerd-${LINKERD_VERSION}"
if [ ! -f "${LINKERD_BIN}" ]; then
  curl -sL "https://github.com/linkerd/linkerd2/releases/download/${LINKERD_VERSION}/linkerd2-cli-${LINKERD_VERSION}-linux-amd64" \
    -o "${LINKERD_BIN}"
  chmod +x "${LINKERD_BIN}"
fi

echo "==> Installing Linkerd CRDs..."
"${LINKERD_BIN}" install --crds | kubectl apply --context "kind-${CLUSTER_NAME}" -f -

echo "==> Installing Linkerd control plane..."
"${LINKERD_BIN}" install | kubectl apply --context "kind-${CLUSTER_NAME}" -f -

echo "==> Waiting for Linkerd control plane..."
"${LINKERD_BIN}" check --context "kind-${CLUSTER_NAME}" --wait 120s || true

echo "==> Building and loading controller image..."
make docker-build
kind load docker-image divergedev/diverge:latest --name "${CLUSTER_NAME}"

echo "==> Deploying Diverge CRDs and controller..."
kubectl apply -f config/crd/bases/ --context "kind-${CLUSTER_NAME}"
kubectl apply -k config/default --context "kind-${CLUSTER_NAME}" || true
kubectl -n diverge-system wait --for=condition=available \
  deployment/diverge-controller --timeout=120s \
  --context "kind-${CLUSTER_NAME}" || true

echo "==> Linkerd E2E setup complete!"
