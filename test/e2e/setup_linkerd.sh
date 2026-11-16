#!/usr/bin/env bash
# Setup script for Linkerd GAMMA conformance E2E tests.
# Creates a Kind cluster with Linkerd service mesh + Gateway API.
set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-diverge-linkerd}"
LINKERD_VERSION="${LINKERD_VERSION:-edge-26.11.2}"
GWAPI_VERSION="${GWAPI_VERSION:-v1.2.1}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "==> Creating Kind cluster '${CLUSTER_NAME}'..."
kind get clusters | grep -qx "${CLUSTER_NAME}" || \
  kind create cluster --name "${CLUSTER_NAME}" --config "${SCRIPT_DIR}/kind-config.yaml"

echo "==> Installing Gateway API CRDs ${GWAPI_VERSION}..."
kubectl apply --context "kind-${CLUSTER_NAME}" \
  -f "https://github.com/kubernetes-sigs/gateway-api/releases/download/${GWAPI_VERSION}/experimental-install.yaml"

echo "==> Installing Linkerd CLI ${LINKERD_VERSION}..."
LINKERD_BIN="/tmp/linkerd-${LINKERD_VERSION}"
if [ ! -f "${LINKERD_BIN}" ]; then
  OS=$(uname -s | tr '[:upper:]' '[:lower:]')
  ARCH=$(uname -m)
  if [ "${OS}" = "darwin" ]; then
    if [ "${ARCH}" = "arm64" ]; then
      ASSET_SUFFIX="darwin-arm64"
    else
      ASSET_SUFFIX="darwin"
    fi
  else
    if [ "${ARCH}" = "aarch64" ] || [ "${ARCH}" = "arm64" ]; then
      ASSET_SUFFIX="linux-arm64"
    else
      ASSET_SUFFIX="linux-amd64"
    fi
  fi
  curl -fsSL "https://github.com/linkerd/linkerd2/releases/download/${LINKERD_VERSION}/linkerd2-cli-${LINKERD_VERSION}-${ASSET_SUFFIX}" \
    -o "${LINKERD_BIN}"
  chmod +x "${LINKERD_BIN}"
fi

echo "==> Generating Linkerd mTLS trust anchor and issuer certificates..."
CERT_DIR="$(mktemp -d)"
TLS_DIR="$(mktemp -d)"
cleanup() {
  local rc=$?
  rm -rf "${CERT_DIR}" "${TLS_DIR}"
  exit "${rc}"
}
trap cleanup EXIT
openssl ecparam -name prime256v1 -genkey -noout -out "${CERT_DIR}/ca.key"
openssl req -x509 -new -nodes -key "${CERT_DIR}/ca.key" -sha256 -days 365 \
  -subj "/CN=root.linkerd.cluster.local" \
  -addext "basicConstraints=critical,CA:TRUE" \
  -addext "keyUsage=critical,keyCertSign,cRLSign" \
  -out "${CERT_DIR}/ca.crt" 2>/dev/null
openssl ecparam -name prime256v1 -genkey -noout -out "${CERT_DIR}/issuer.key"
openssl req -new -key "${CERT_DIR}/issuer.key" \
  -subj "/CN=identity.linkerd.cluster.local" \
  -out "${CERT_DIR}/issuer.csr" 2>/dev/null
cat > "${CERT_DIR}/issuer.ext" <<'EXT'
basicConstraints=critical,CA:TRUE,pathlen:0
keyUsage=critical,keyCertSign,cRLSign
EXT
openssl x509 -req -in "${CERT_DIR}/issuer.csr" \
  -CA "${CERT_DIR}/ca.crt" -CAkey "${CERT_DIR}/ca.key" -CAcreateserial \
  -days 365 -sha256 -extfile "${CERT_DIR}/issuer.ext" \
  -out "${CERT_DIR}/issuer.crt" 2>/dev/null

echo "==> Installing Linkerd CRDs..."
"${LINKERD_BIN}" install --crds --ignore-cluster --set installGatewayAPI=false | \
  kubectl apply --context "kind-${CLUSTER_NAME}" -f -

echo "==> Installing Linkerd control plane..."
"${LINKERD_BIN}" install --ignore-cluster \
  --identity-trust-anchors-file "${CERT_DIR}/ca.crt" \
  --identity-issuer-certificate-file "${CERT_DIR}/issuer.crt" \
  --identity-issuer-key-file "${CERT_DIR}/issuer.key" \
  --set proxyInit.runAsRoot=true | \
  kubectl apply --context "kind-${CLUSTER_NAME}" -f -
rm -rf "${CERT_DIR}"

echo "==> Waiting for Linkerd control plane..."
kubectl -n linkerd rollout status deploy/linkerd-identity --context "kind-${CLUSTER_NAME}" --timeout=180s
kubectl -n linkerd rollout status deploy/linkerd-destination --context "kind-${CLUSTER_NAME}" --timeout=180s
kubectl -n linkerd rollout status deploy/linkerd-proxy-injector --context "kind-${CLUSTER_NAME}" --timeout=180s

echo "==> Building and loading controller image..."
make docker-build
kind load docker-image divergedev/diverge:latest --name "${CLUSTER_NAME}"

echo "==> Deploying Diverge CRDs and controller..."
kubectl apply -f config/crd/bases/ --context "kind-${CLUSTER_NAME}"
kubectl create namespace diverge-system --dry-run=client -o yaml | kubectl apply --context "kind-${CLUSTER_NAME}" -f -
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

echo "==> Linkerd E2E setup complete!"
