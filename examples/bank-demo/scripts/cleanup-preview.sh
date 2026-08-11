#!/usr/bin/env bash
# Cleanup a preview environment
# Usage: ./cleanup-preview.sh [preview-id]

set -euo pipefail

DEMO_NS="demo-bank"
PREVIEW_ID="${1:-42}"

GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m'

log() { echo -e "${BLUE}[diverge-demo]${NC} $*"; }
ok()  { echo -e "${GREEN}[✅]${NC} $*"; }

log "Cleaning up preview ${PREVIEW_ID}..."

kubectl delete deployment -n "$DEMO_NS" -l "diverge.io/preview-id=${PREVIEW_ID}" 2>/dev/null || true
kubectl delete service    -n "$DEMO_NS" -l "diverge.io/preview-id=${PREVIEW_ID}" 2>/dev/null || true
kubectl delete httproute  -n "$DEMO_NS" -l "diverge.io/preview-id=${PREVIEW_ID}" 2>/dev/null || true

ok "Preview ${PREVIEW_ID} cleaned up"
