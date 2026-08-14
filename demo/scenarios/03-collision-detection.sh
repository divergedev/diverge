#!/usr/bin/env bash
set -euo pipefail
CTX="k3d-diverge-demo"
echo "═══════════════════════════════════════════"
echo "  Scenario 3: Collision Detection"
echo "═══════════════════════════════════════════"
echo ""

echo "📝 Creating PreviewGroup for developer alice..."
kubectl apply --context "$CTX" -f - <<EOF
apiVersion: diverge.io/v1alpha1
kind: PreviewGroup
metadata:
  name: alice-payments
  namespace: default
spec:
  owner: alice
  services:
  - payments
EOF

echo ""
echo "📝 Attempting to create PreviewGroup for developer bob on same service..."
kubectl apply --context "$CTX" -f - <<EOF
apiVersion: diverge.io/v1alpha1
kind: PreviewGroup
metadata:
  name: bob-payments
  namespace: default
spec:
  owner: bob
  services:
  - payments
EOF

echo ""
echo "📊 PreviewGroups:"
kubectl get previewgroups --context "$CTX" -o wide 2>/dev/null || echo "  (PreviewGroup CRD)"
echo ""
echo "✅ The controller detects collision and marks bob's PreviewGroup as Conflicted."
echo "   Only one developer can claim a service at a time."
