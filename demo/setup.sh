#!/usr/bin/env bash
set -euo pipefail

CLUSTER_NAME="diverge-demo"
IMG="divergedev/diverge:demo"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

BOLD='\033[1m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
NC='\033[0m'

step() { echo -e "\n${BOLD}${CYAN}▸ $1${NC}"; }

# ─────────────────────────────────────────────────────────
step "1/6 Creating k3d cluster '${CLUSTER_NAME}'..."
if k3d cluster list 2>/dev/null | grep -q "$CLUSTER_NAME"; then
  echo "  Cluster already exists, reusing."
else
  k3d cluster create "$CLUSTER_NAME" \
    --api-port 6550 \
    --port "8080:80@loadbalancer" \
    --port "8443:443@loadbalancer" \
    --k3s-arg "--disable=traefik@server:0" \
    --wait
fi

# ─────────────────────────────────────────────────────────
step "2/7 Installing Gateway API + Envoy Gateway..."
helm install eg oci://docker.io/envoyproxy/gateway-helm \
  --version v1.2.6 \
  --namespace envoy-gateway-system --create-namespace \
  --kube-context "k3d-${CLUSTER_NAME}" \
  --wait --timeout 120s
echo "  Waiting for Envoy Gateway to be ready..."
kubectl wait --for=condition=available deployment/envoy-gateway \
  --namespace envoy-gateway-system --timeout=60s --context "k3d-${CLUSTER_NAME}" 2>/dev/null || true

# ─────────────────────────────────────────────────────────
step "3/7 Installing Diverge CRDs..."
kubectl apply -f "${ROOT_DIR}/config/crd/bases/" --context "k3d-${CLUSTER_NAME}"

# ─────────────────────────────────────────────────────────
step "4/7 Building Diverge controller image..."
cd "$ROOT_DIR"
docker build -t "$IMG" . --quiet
k3d image import "$IMG" -c "$CLUSTER_NAME"

# ─────────────────────────────────────────────────────────
step "5/7 Deploying Diverge controller + proxy..."

# Create self-signed webhook TLS secret for the demo
openssl req -x509 -newkey rsa:2048 -keyout /tmp/diverge-tls.key -out /tmp/diverge-tls.crt \
  -days 1 -nodes -subj "/CN=diverge-webhook.default.svc" 2>/dev/null
kubectl create secret tls diverge-webhook-tls \
  --cert=/tmp/diverge-tls.crt --key=/tmp/diverge-tls.key \
  --context "k3d-${CLUSTER_NAME}" 2>/dev/null || true
rm -f /tmp/diverge-tls.key /tmp/diverge-tls.crt

helm upgrade --install diverge "${ROOT_DIR}/charts/diverge" \
  --set image.repository=divergedev/diverge \
  --set image.tag=demo \
  --set image.pullPolicy=Never \
  --set routingProvider=composite \
  --kube-context "k3d-${CLUSTER_NAME}" \
  --wait --timeout 120s

# ─────────────────────────────────────────────────────────
step "6/7 Deploying sample microservices..."
kubectl apply -f "${SCRIPT_DIR}/manifests/services.yaml" --context "k3d-${CLUSTER_NAME}"
kubectl apply -f "${SCRIPT_DIR}/manifests/gateway.yaml" --context "k3d-${CLUSTER_NAME}"

step "7/7 Waiting for Gateway to be programmed..."
kubectl wait --for=condition=programmed gateway/diverge-gateway \
  --timeout=60s --context "k3d-${CLUSTER_NAME}" 2>/dev/null || true

# Wait for pods
echo -e "  ${YELLOW}Waiting for services to be ready...${NC}"
kubectl wait --for=condition=available deployment/frontend deployment/payments deployment/orders \
  --timeout=60s --context "k3d-${CLUSTER_NAME}" 2>/dev/null || true

echo ""
echo -e "${BOLD}${GREEN}╔═══════════════════════════════════════════════╗${NC}"
echo -e "${BOLD}${GREEN}║  🔀 Diverge Demo Ready!                      ║${NC}"
echo -e "${BOLD}${GREEN}╠═══════════════════════════════════════════════╣${NC}"
echo -e "${BOLD}${GREEN}║                                               ║${NC}"
echo -e "${BOLD}${GREEN}║  make demo-killer      # The headline demo    ║${NC}"
echo -e "${BOLD}${GREEN}║  make demo-scenario-1  # Preview routing      ║${NC}"
echo -e "${BOLD}${GREEN}║  make demo-scenario-2  # GAMMA mesh routing   ║${NC}"
echo -e "${BOLD}${GREEN}║  make demo-scenario-3  # Collision detection  ║${NC}"
echo -e "${BOLD}${GREEN}║  make demo-scenario-4  # Cleanup              ║${NC}"
echo -e "${BOLD}${GREEN}║                                               ║${NC}"
echo -e "${BOLD}${GREEN}║  make demo-teardown    # Destroy cluster      ║${NC}"
echo -e "${BOLD}${GREEN}╚═══════════════════════════════════════════════╝${NC}"
echo ""
echo -e "${CYAN}Controller:${NC}"
kubectl get pods -l app.kubernetes.io/name=diverge --context "k3d-${CLUSTER_NAME}" 2>/dev/null || true
echo ""
echo -e "${CYAN}Services:${NC}"
kubectl get pods -l "app in (frontend,payments,orders)" --context "k3d-${CLUSTER_NAME}" 2>/dev/null || true
