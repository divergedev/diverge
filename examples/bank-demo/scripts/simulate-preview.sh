#!/usr/bin/env bash
# Simulate a preview environment for payments-api
# This mimics what happens when a developer opens an MR:
# 1. Builds a "preview" version of payments-api with changes
# 2. Loads it into k3d
# 3. Deploys a preview pod alongside the baseline
# 4. Creates an HTTPRoute that routes x-preview-id:42 to the preview pod
#
# Usage: ./simulate-preview.sh [preview-id]

set -euo pipefail

CLUSTER_NAME="diverge-demo"
DEMO_NS="demo-bank"
PREVIEW_ID="${1:-42}"
PREVIEW_NAME="payments-api-preview-${PREVIEW_ID}"

# Colors
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m'

log() { echo -e "${BLUE}[diverge-demo]${NC} $*"; }
ok()  { echo -e "${GREEN}[✅]${NC} $*"; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
PARENT_DIR="$(dirname "$REPO_ROOT")"

# ─── 1. Build preview image with "changes" ───────────────
log "Building preview image for payments-api..."

# Create a temporary modified version that shows it's the preview
TMPDIR=$(mktemp -d)
cp -r "${PARENT_DIR}/demo-payments-api/"* "$TMPDIR/"
# Patch the default version to show this is a preview build
sed -i.bak "s/version = \"baseline\"/version = \"preview-${PREVIEW_ID}\"/" "$TMPDIR/main.go" 2>/dev/null || \
  sed -i '' "s/version = \"baseline\"/version = \"preview-${PREVIEW_ID}\"/" "$TMPDIR/main.go"

IMAGE="divergedev/demo-payments-api:preview-${PREVIEW_ID}"
docker build -t "$IMAGE" "$TMPDIR" --quiet
k3d image import "$IMAGE" -c "$CLUSTER_NAME"
rm -rf "$TMPDIR"
ok "Preview image built: $IMAGE"

# ─── 2. Deploy preview pod ───────────────────────────────
log "Deploying preview pod..."
kubectl apply -n "$DEMO_NS" -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ${PREVIEW_NAME}
  labels:
    app: payments-api
    diverge.io/role: preview
    diverge.io/preview-id: "${PREVIEW_ID}"
    diverge.io/environment: "mr-${PREVIEW_ID}"
spec:
  replicas: 1
  selector:
    matchLabels:
      app: payments-api
      diverge.io/preview-id: "${PREVIEW_ID}"
  template:
    metadata:
      labels:
        app: payments-api
        diverge.io/role: preview
        diverge.io/preview-id: "${PREVIEW_ID}"
    spec:
      containers:
        - name: payments-api
          image: ${IMAGE}
          imagePullPolicy: Never
          ports:
            - containerPort: 8080
          env:
            - name: APP_VERSION
              value: "preview-${PREVIEW_ID}"
            - name: ACCOUNTS_API_URL
              value: http://accounts-api:8080
          readinessProbe:
            httpGet:
              path: /health
              port: 8080
---
apiVersion: v1
kind: Service
metadata:
  name: ${PREVIEW_NAME}
  labels:
    diverge.io/role: preview
    diverge.io/preview-id: "${PREVIEW_ID}"
spec:
  selector:
    app: payments-api
    diverge.io/preview-id: "${PREVIEW_ID}"
  ports:
    - port: 8080
      targetPort: 8080
EOF
ok "Preview pod deployed: ${PREVIEW_NAME}"

# ─── 3. Create preview HTTPRoute ─────────────────────────
log "Creating preview HTTPRoute..."
kubectl apply -n "$DEMO_NS" -f - <<EOF
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: preview-${PREVIEW_ID}-payments
  labels:
    diverge.io/role: preview
    diverge.io/preview-id: "${PREVIEW_ID}"
spec:
  parentRefs:
    - name: demo-gateway
  rules:
    # Preview route: header match takes priority
    - matches:
        - path:
            type: PathPrefix
            value: /api/payments
          headers:
            - name: x-preview-id
              value: "${PREVIEW_ID}"
      backendRefs:
        - name: ${PREVIEW_NAME}
          port: 8080
EOF
ok "Preview HTTPRoute created"

# ─── 4. Wait for preview pod ─────────────────────────────
log "Waiting for preview pod..."
kubectl wait --for=condition=Ready pod \
    -l "diverge.io/preview-id=${PREVIEW_ID}" \
    -n "$DEMO_NS" --timeout=60s 2>/dev/null || true

# ─── 5. Print test commands ──────────────────────────────
echo ""
echo -e "${GREEN}════════════════════════════════════════════════════${NC}"
echo -e "${GREEN}  Preview Environment Ready! (preview-id: ${PREVIEW_ID})${NC}"
echo -e "${GREEN}════════════════════════════════════════════════════${NC}"
echo ""
echo -e "  ${YELLOW}Test baseline (no header):${NC}"
echo -e "    curl -s http://localhost:8080/api/payments | jq '.version'"
echo -e "    → Expected: ${BLUE}\"baseline\"${NC}"
echo ""
echo -e "  ${YELLOW}Test preview (with header):${NC}"
echo -e "    curl -s -H 'x-preview-id: ${PREVIEW_ID}' http://localhost:8080/api/payments | jq '.version'"
echo -e "    → Expected: ${BLUE}\"preview-${PREVIEW_ID}\"${NC}"
echo ""
echo -e "  ${YELLOW}Full comparison:${NC}"
echo -e "    echo '--- Baseline ---' && curl -s http://localhost:8080/api/payments | jq '{version, service, preview}'"
echo -e "    echo '--- Preview ---'  && curl -s -H 'x-preview-id: ${PREVIEW_ID}' http://localhost:8080/api/payments | jq '{version, service, preview}'"
echo ""
echo -e "  ${YELLOW}Cleanup this preview:${NC}"
echo -e "    ./cleanup-preview.sh ${PREVIEW_ID}"
echo ""
