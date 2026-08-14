#!/usr/bin/env bash
set -euo pipefail

# Required args
PROJECT="${GCP_PROJECT:?Set GCP_PROJECT}"
REGION="${GCP_REGION:-us-central1}"
CLUSTER_NAME="${GKE_CLUSTER:-diverge-demo}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
IMG="us-docker.pkg.dev/${PROJECT}/diverge/controller:demo"

BOLD='\033[1m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
NC='\033[0m'

step() { echo -e "\n${BOLD}${CYAN}▸ $1${NC}"; }

# TLS temp directory with cleanup trap
TLS_DIR=$(mktemp -d)
trap 'rm -rf "$TLS_DIR"' EXIT

# 0. Enable required APIs (opt-in)
if [[ "${ENABLE_APIS:-}" == "1" ]]; then
  step "0/7 Enabling required GCP APIs..."
  gcloud services enable \
    container.googleapis.com \
    artifactregistry.googleapis.com \
    --project="$PROJECT"
fi

# 1. Create GKE Autopilot cluster
step "1/7 Creating GKE Autopilot cluster '${CLUSTER_NAME}'..."
if gcloud container clusters describe "$CLUSTER_NAME" --region="$REGION" --project="$PROJECT" &>/dev/null; then
  echo "  Cluster already exists, reusing."
else
  # Ensure a VPC network exists (some projects have no default network)
  NETWORK="${GKE_NETWORK:-diverge-demo}"
  if ! gcloud compute networks describe "$NETWORK" --project="$PROJECT" &>/dev/null; then
    echo "  Creating VPC network '${NETWORK}'..."
    gcloud compute networks create "$NETWORK" \
      --subnet-mode=auto --project="$PROJECT"
  fi

  gcloud container clusters create-auto "$CLUSTER_NAME" \
    --region="$REGION" \
    --project="$PROJECT" \
    --network="$NETWORK" \
    --release-channel=rapid
fi

# Get credentials
gcloud container clusters get-credentials "$CLUSTER_NAME" \
  --region="$REGION" --project="$PROJECT"
CTX="gke_${PROJECT}_${REGION}_${CLUSTER_NAME}"

# 2. Enable Gateway API (GKE has built-in GatewayClass)
step "2/7 Enabling Gateway API on GKE..."
kubectl get gatewayclass --context "$CTX" 2>/dev/null || \
  echo "  Gateway API CRDs will be available shortly..."

# 3. Install Diverge CRDs
step "3/7 Installing Diverge CRDs..."
kubectl apply -f "${ROOT_DIR}/config/crd/bases/" --context "$CTX"

# 4. Build and push controller image
step "4/7 Building and pushing controller image..."
# Create Artifact Registry repo if needed
gcloud artifacts repositories describe diverge \
  --location=us --project="$PROJECT" &>/dev/null || \
gcloud artifacts repositories create diverge \
  --repository-format=docker --location=us --project="$PROJECT"

# Configure Docker auth for Artifact Registry
gcloud auth configure-docker us-docker.pkg.dev --quiet

cd "$ROOT_DIR"
docker buildx build --platform linux/amd64 -t "$IMG" --push .

# 5. Deploy controller
step "5/7 Deploying Diverge controller..."

# Create self-signed webhook TLS secret
openssl req -x509 -newkey rsa:2048 \
  -keyout "${TLS_DIR}/tls.key" -out "${TLS_DIR}/tls.crt" \
  -days 1 -nodes -subj "/CN=diverge-webhook.default.svc" 2>/dev/null
kubectl create secret tls diverge-webhook-tls \
  --cert="${TLS_DIR}/tls.crt" --key="${TLS_DIR}/tls.key" \
  --context "$CTX" 2>/dev/null || true

helm upgrade --install diverge "${ROOT_DIR}/charts/diverge" \
  --set image.repository="us-docker.pkg.dev/${PROJECT}/diverge/controller" \
  --set image.tag=demo \
  --set image.pullPolicy=Always \
  --set routingProvider=composite \
  --kube-context "$CTX" \
  --wait --timeout 300s

# 6. Deploy sample services
step "6/7 Deploying sample microservices..."
kubectl apply -f "${SCRIPT_DIR}/manifests/services.yaml" --context "$CTX"

# Create GKE-specific gateway using built-in GatewayClass
kubectl apply --context "$CTX" -f - <<EOF
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: diverge-gateway
  namespace: default
spec:
  gatewayClassName: gke-l7-global-external-managed
  listeners:
  - name: http
    protocol: HTTP
    port: 80
EOF

# 7. Wait for everything
step "7/7 Waiting for Gateway + services..."
kubectl wait --for=condition=programmed gateway/diverge-gateway \
  --timeout=180s --context "$CTX"
kubectl wait --for=condition=available deployment/frontend deployment/payments deployment/orders \
  --timeout=120s --context "$CTX"

# Get external IP
EXTERNAL_IP=$(kubectl get gateway diverge-gateway --context "$CTX" \
  -o jsonpath='{.status.addresses[0].value}' 2>/dev/null || echo "pending")

echo ""
echo -e "${BOLD}${GREEN}╔═══════════════════════════════════════════════════════╗${NC}"
echo -e "${BOLD}${GREEN}║  🔀 Diverge GKE Demo Ready!                          ║${NC}"
echo -e "${BOLD}${GREEN}╠═══════════════════════════════════════════════════════╣${NC}"
echo -e "${BOLD}${GREEN}║                                                       ║${NC}"
echo -e "${BOLD}${GREEN}║  DIVERGE_DEMO_CTX=${CTX}${NC}"
echo -e "${BOLD}${GREEN}║  DIVERGE_DEMO_URL=http://${EXTERNAL_IP}${NC}"
echo -e "${BOLD}${GREEN}║                                                       ║${NC}"
echo -e "${BOLD}${GREEN}║  Run:                                                 ║${NC}"
echo -e "${BOLD}${GREEN}║    make demo-gke-killer    # The headline demo        ║${NC}"
echo -e "${BOLD}${GREEN}║    make demo-gke-teardown  # Destroy GKE cluster      ║${NC}"
echo -e "${BOLD}${GREEN}╚═══════════════════════════════════════════════════════╝${NC}"
echo ""
echo -e "${CYAN}Controller:${NC}"
kubectl get pods -l app.kubernetes.io/name=diverge --context "$CTX" 2>/dev/null || true
echo ""
echo -e "${CYAN}Services:${NC}"
kubectl get pods -l "app in (frontend,payments,orders)" --context "$CTX" 2>/dev/null || true
