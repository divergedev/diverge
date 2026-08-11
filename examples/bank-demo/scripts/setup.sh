#!/usr/bin/env bash
# Diverge Bank Demo — Full Setup Script
# Creates a k3d cluster with Envoy Gateway, deploys baseline services,
# and demonstrates multi-repo preview environments with header-based routing.
#
# Usage: ./setup.sh
# Teardown: ./setup.sh teardown

set -euo pipefail

CLUSTER_NAME="diverge-demo"
DEMO_NS="demo-bank"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
DEMO_SERVICES_DIR="$(cd "$SCRIPT_DIR/.." 2>/dev/null && pwd)"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m'

log() { echo -e "${BLUE}[diverge-demo]${NC} $*"; }
ok()  { echo -e "${GREEN}[✅]${NC} $*"; }
warn(){ echo -e "${YELLOW}[⚠️]${NC} $*"; }
err() { echo -e "${RED}[❌]${NC} $*"; exit 1; }

# ─── Teardown ──────────────────────────────────────────────
if [[ "${1:-}" == "teardown" ]]; then
    log "Tearing down demo cluster..."
    k3d cluster delete "$CLUSTER_NAME" 2>/dev/null || true
    ok "Cluster deleted"
    exit 0
fi

# ─── Prerequisites ─────────────────────────────────────────
command -v k3d    >/dev/null || err "k3d not found. Install: https://k3d.io"
command -v kubectl >/dev/null || err "kubectl not found"
command -v helm    >/dev/null || err "helm not found"
command -v docker  >/dev/null || err "docker not found"

# ─── 1. Create k3d cluster ─────────────────────────────────
if k3d cluster list | grep -q "$CLUSTER_NAME"; then
    warn "Cluster '$CLUSTER_NAME' already exists, reusing"
else
    log "Creating k3d cluster..."
    k3d cluster create "$CLUSTER_NAME" \
        --port "8080:80@loadbalancer" \
        --port "8443:443@loadbalancer" \
        --k3s-arg "--disable=traefik@server:0" \
        --wait
    ok "k3d cluster created"
fi

kubectl config use-context "k3d-${CLUSTER_NAME}"

# ─── 2. Install Gateway API CRDs ──────────────────────────
log "Installing Gateway API CRDs..."
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.2.1/standard-install.yaml 2>/dev/null
ok "Gateway API CRDs installed"

# ─── 3. Install Envoy Gateway ─────────────────────────────
log "Installing Envoy Gateway..."
helm upgrade --install eg oci://docker.io/envoyproxy/gateway-helm \
    --version v1.2.4 \
    --namespace envoy-gateway-system \
    --create-namespace \
    --wait 2>/dev/null
ok "Envoy Gateway installed"

# ─── 4. Create demo namespace ─────────────────────────────
log "Creating namespace '$DEMO_NS'..."
kubectl create namespace "$DEMO_NS" --dry-run=client -o yaml | kubectl apply -f -
ok "Namespace ready"

# ─── 5. Create Gateway in demo namespace ──────────────────
log "Creating Gateway..."
kubectl apply -f - <<'EOF'
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: diverge-demo
spec:
  controllerName: gateway.envoyproxy.io/gatewayclass-controller
---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: demo-gateway
  namespace: demo-bank
spec:
  gatewayClassName: diverge-demo
  listeners:
    - name: http
      protocol: HTTP
      port: 80
EOF
ok "Gateway created"

# ─── 6. Build and load demo service images ────────────────
log "Building demo service images..."

SERVICES=(demo-payments-api demo-accounts-api demo-gateway demo-web-app)
PARENT_DIR="$(dirname "$REPO_ROOT")"

for svc in "${SERVICES[@]}"; do
    SVC_DIR="${PARENT_DIR}/${svc}"
    if [[ ! -d "$SVC_DIR" ]]; then
        warn "Service dir $SVC_DIR not found, skipping"
        continue
    fi
    IMAGE="divergedev/${svc}:baseline"
    log "  Building $IMAGE..."
    docker build -t "$IMAGE" "$SVC_DIR" --quiet
    k3d image import "$IMAGE" -c "$CLUSTER_NAME"
    ok "  $svc built and loaded"
done

# ─── 7. Deploy baseline services ─────────────────────────
log "Deploying baseline services..."

# Accounts API (no dependencies)
kubectl apply -n "$DEMO_NS" -f - <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: accounts-api
  labels:
    app: accounts-api
    diverge.io/role: baseline
spec:
  replicas: 1
  selector:
    matchLabels:
      app: accounts-api
  template:
    metadata:
      labels:
        app: accounts-api
        diverge.io/role: baseline
    spec:
      containers:
        - name: accounts-api
          image: divergedev/demo-accounts-api:baseline
          imagePullPolicy: Never
          ports:
            - containerPort: 8080
          env:
            - name: APP_VERSION
              value: baseline
          readinessProbe:
            httpGet:
              path: /health
              port: 8080
---
apiVersion: v1
kind: Service
metadata:
  name: accounts-api
spec:
  selector:
    app: accounts-api
  ports:
    - port: 8080
      targetPort: 8080
EOF

# Payments API
kubectl apply -n "$DEMO_NS" -f - <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: payments-api
  labels:
    app: payments-api
    diverge.io/role: baseline
spec:
  replicas: 1
  selector:
    matchLabels:
      app: payments-api
  template:
    metadata:
      labels:
        app: payments-api
        diverge.io/role: baseline
    spec:
      containers:
        - name: payments-api
          image: divergedev/demo-payments-api:baseline
          imagePullPolicy: Never
          ports:
            - containerPort: 8080
          env:
            - name: APP_VERSION
              value: baseline
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
  name: payments-api
spec:
  selector:
    app: payments-api
  ports:
    - port: 8080
      targetPort: 8080
EOF

# Gateway (BFF)
kubectl apply -n "$DEMO_NS" -f - <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: gateway
  labels:
    app: gateway
    diverge.io/role: baseline
spec:
  replicas: 1
  selector:
    matchLabels:
      app: gateway
  template:
    metadata:
      labels:
        app: gateway
        diverge.io/role: baseline
    spec:
      containers:
        - name: gateway
          image: divergedev/demo-gateway:baseline
          imagePullPolicy: Never
          ports:
            - containerPort: 8080
          env:
            - name: APP_VERSION
              value: baseline
            - name: PAYMENTS_API_URL
              value: http://payments-api:8080
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
  name: gateway
spec:
  selector:
    app: gateway
  ports:
    - port: 8080
      targetPort: 8080
EOF

# Web App
kubectl apply -n "$DEMO_NS" -f - <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web-app
  labels:
    app: web-app
    diverge.io/role: baseline
spec:
  replicas: 1
  selector:
    matchLabels:
      app: web-app
  template:
    metadata:
      labels:
        app: web-app
        diverge.io/role: baseline
    spec:
      containers:
        - name: web-app
          image: divergedev/demo-web-app:baseline
          imagePullPolicy: Never
          ports:
            - containerPort: 8080
          env:
            - name: APP_VERSION
              value: baseline
            - name: GATEWAY_URL
              value: http://gateway:8080
          readinessProbe:
            httpGet:
              path: /health
              port: 8080
---
apiVersion: v1
kind: Service
metadata:
  name: web-app
spec:
  selector:
    app: web-app
  ports:
    - port: 8080
      targetPort: 8080
EOF

ok "Baseline services deployed"

# ─── 8. Create baseline HTTPRoute ─────────────────────────
log "Creating baseline HTTPRoute..."
kubectl apply -n "$DEMO_NS" -f - <<'EOF'
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: baseline-routes
spec:
  parentRefs:
    - name: demo-gateway
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /api/payments
      backendRefs:
        - name: gateway
          port: 8080
    - matches:
        - path:
            type: PathPrefix
            value: /api/accounts
      backendRefs:
        - name: gateway
          port: 8080
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - name: web-app
          port: 8080
EOF
ok "Baseline HTTPRoutes created"

# ─── 9. Wait for pods ────────────────────────────────────
log "Waiting for pods to be ready..."
kubectl wait --for=condition=Ready pod -l diverge.io/role=baseline \
    -n "$DEMO_NS" --timeout=120s 2>/dev/null || warn "Some pods not ready yet"
ok "Baseline environment ready"

# ─── 10. Print summary ───────────────────────────────────
echo ""
echo -e "${GREEN}════════════════════════════════════════════════════${NC}"
echo -e "${GREEN}  Diverge Bank Demo — Ready!${NC}"
echo -e "${GREEN}════════════════════════════════════════════════════${NC}"
echo ""
echo -e "  ${BLUE}Baseline URL:${NC}  http://localhost:8080/"
echo -e "  ${BLUE}Payments API:${NC}  http://localhost:8080/api/payments"
echo -e "  ${BLUE}Accounts API:${NC}  http://localhost:8080/api/accounts"
echo ""
echo -e "  ${YELLOW}Simulate a preview:${NC}"
echo -e "    ./simulate-preview.sh"
echo ""
echo -e "  ${YELLOW}Test routing:${NC}"
echo -e "    # Baseline (no header)"
echo -e "    curl http://localhost:8080/api/payments | jq"
echo ""
echo -e "    # Preview (with header)"
echo -e "    curl -H 'x-preview-id: 42' http://localhost:8080/api/payments | jq"
echo ""
echo -e "  ${YELLOW}Teardown:${NC}"
echo -e "    ./setup.sh teardown"
echo ""
