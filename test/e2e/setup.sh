#!/usr/bin/env bash
set -euo pipefail

echo "Setting up e2e environment in cluster k3d-oneazra-dev..."

# 1. Switch context
kubectl config use-context k3d-oneazra-dev

# 2. Install Diverge CRDs
echo "Installing Diverge CRDs..."
kubectl apply -f config/crd/bases/

# 3. Install Gateway API CRDs
echo "Installing Gateway API CRDs..."
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.1.0/standard-install.yaml

# 4. Create test namespace
echo "Creating test namespace diverge-e2e-test..."
kubectl apply -f - <<EOF
apiVersion: v1
kind: Namespace
metadata:
  name: diverge-e2e-test
EOF

# 5. Deploy echo server
echo "Deploying echo server..."
kubectl apply -n diverge-e2e-test -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: echo-server
spec:
  replicas: 1
  selector:
    matchLabels:
      app: echo-server
  template:
    metadata:
      labels:
        app: echo-server
    spec:
      containers:
      - name: echo-server
        image: ealen/echo-server:latest
        ports:
        - containerPort: 80
---
apiVersion: v1
kind: Service
metadata:
  name: echo-server
spec:
  selector:
    app: echo-server
  ports:
  - port: 80
    targetPort: 80
EOF

echo "Waiting for echo-server to be ready..."
kubectl wait --namespace diverge-e2e-test \
  --for=condition=ready pod \
  --selector=app=echo-server \
  --timeout=90s

echo "E2E Setup complete!"
