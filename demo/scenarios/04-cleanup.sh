#!/usr/bin/env bash
set -euo pipefail
echo "═══════════════════════════════════════════"
echo "  Scenario 4: Cleanup & Dead Man's Switch"
echo "═══════════════════════════════════════════"
echo ""
echo "🧹 Cleaning up all preview resources..."
kubectl delete environments --all 2>/dev/null || true
kubectl delete previewgroups --all 2>/dev/null || true
echo ""
echo "📊 Remaining HTTPRoutes:"
kubectl get httproute -l diverge.io/managed-by=diverge 2>/dev/null || echo "  None — all cleaned up!"
echo ""
echo "✅ Dead man's switch:"
echo "   When a developer's lease expires (90s without heartbeat):"
echo "   1. HTTPRoute/GRPCRoute deleted first (stop routing)"
echo "   2. EndpointSlice deleted (detach dev machine)"
echo "   3. PreviewGroup marked Abandoned and garbage collected"
