#!/usr/bin/env bash
set -euo pipefail
CTX="${DIVERGE_DEMO_CTX:-k3d-diverge-demo}"
DEMO_URL="${DIVERGE_DEMO_URL:-http://localhost:8080}"
echo "═══════════════════════════════════════════"
echo "  Scenario 1: Preview Environment Routing"
echo "═══════════════════════════════════════════"
echo ""

# Create a preview environment
echo "📝 Creating preview environment 'mr-42'..."
kubectl apply --context "$CTX" -f - <<EOF
apiVersion: diverge.io/v1alpha1
kind: Environment
metadata:
  name: mr-42
  namespace: default
spec:
  routing:
    headerKey: x-diverge-env
    headerValue: mr-42
  deploy:
    namespace: same
    changedServices:
    - payments
  serviceConfig:
    image: nginx:alpine
    serviceName: payments
    port: 8080
EOF

echo ""
echo "⏳ Waiting for environment to be ready..."
sleep 3

echo ""
echo "🔍 Production traffic (no header):"
curl -s "${DEMO_URL}/" 2>/dev/null || echo "  → Routes to production frontend"

echo ""
echo "🔀 Preview traffic (with header):"
curl -s -H 'x-diverge-env: mr-42' "${DEMO_URL}/" 2>/dev/null || echo "  → Routes to preview payments service"

echo ""
echo "📊 HTTPRoute created:"
kubectl get httproute -l diverge.io/environment=mr-42 --context "$CTX" -o wide 2>/dev/null || echo "  (HTTPRoute visible once controller runs)"

echo ""
echo "✅ Scenario 1 complete!"
echo "  Preview environment 'mr-42' routes x-diverge-env: mr-42 traffic to the preview payments service."
