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
step "2/6 Installing Gateway API CRDs..."
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.2.1/standard-install.yaml --context "k3d-${CLUSTER_NAME}" 2>/dev/null || \
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.1.0/standard-install.yaml --context "k3d-${CLUSTER_NAME}"

# ─────────────────────────────────────────────────────────
step "3/6 Installing Diverge CRDs..."
kubectl apply -f "${ROOT_DIR}/config/crd/bases/" --context "k3d-${CLUSTER_NAME}"

# ─────────────────────────────────────────────────────────
step "4/6 Building Diverge controller image..."
cd "$ROOT_DIR"
docker build -t "$IMG" . --quiet
k3d image import "$IMG" -c "$CLUSTER_NAME"

# ─────────────────────────────────────────────────────────
step "5/6 Deploying Diverge controller + proxy..."
helm upgrade --install diverge "${ROOT_DIR}/charts/diverge" \
  --set image.repository=divergedev/diverge \
  --set image.tag=demo \
  --set image.pullPolicy=Never \
  --set routingProvider=composite \
  --kube-context "k3d-${CLUSTER_NAME}" \
  --wait --timeout 60s

# ─────────────────────────────────────────────────────────
step "6/6 Deploying sample microservices..."
kubectl apply -f "${SCRIPT_DIR}/manifests/services.yaml" --context "k3d-${CLUSTER_NAME}"
kubectl apply -f "${SCRIPT_DIR}/manifests/gateway.yaml" --context "k3d-${CLUSTER_NAME}"

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
