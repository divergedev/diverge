# 2-Minute Local Quickstart (Kind + Helm)

Spin up a complete local Diverge environment — including `diverge-controller`, `diverge-server` (ConnectRPC + Web UI), Envoy Gateway, and an isolated `PreviewGroup` + `AgentTask` — on your laptop in under 2 minutes.

---

## Prerequisites

- [Docker](https://docs.docker.com/get-docker/)
- [Kind](https://kind.sigs.k8s.io/) (`v0.29+`)
- [kubectl](https://kubernetes.io/docs/tasks/tools/) (`v1.30+`)
- [Helm](https://helm.sh/) (`v3.14+` or `v4+`)
- [Diverge CLI](../../README.md#get-started)

---

## Step 1: Boot Local Cluster & Install Diverge

From a clone of this repository, run the built-in E2E bootstrap target:

```bash
make e2e-setup
```

Or install manually on any Kind/k3d cluster:

```bash
# 1. Create a Kind cluster
kind create cluster --name diverge-local

# 2. Install Envoy Gateway (Gateway API + GRPCRoute support)
helm install eg oci://docker.io/envoyproxy/gateway-helm \
  --version v1.3.0 -n envoy-gateway-system --create-namespace --wait

# 3. Install Diverge Operator + API Server + Dashboard
helm upgrade --install diverge ./charts/diverge \
  --namespace diverge-system --create-namespace \
  --set server.enabled=true \
  --set server.dashboard.enabled=true \
  --set sandbox.enabled=true \
  --set sandbox.provider=noop \
  --wait
```

---

## Step 2: Create Your First Preview Environment

Apply a sample `PreviewGroup` managing two services (`payments-api` on a PR image and `gateway` on baseline):

```bash
kubectl apply -f - <<'EOF'
apiVersion: divergedev.com/v1alpha1
kind: PreviewGroup
metadata:
  name: pr-101
spec:
  ttl: 2h
  source:
    provider: github
    project: acme/payments
    branch: feat/instant-payouts
    mergeRequestID: 101
  routing:
    headerKey: x-preview-env
    headerValue: pr-101
  services:
    - name: payments-api
      namespace: default
      image: hashicorp/http-echo:0.2.3
      mode: image
      port: 5678
    - name: gateway
      namespace: default
      mode: baseline
EOF
```

Check the reconciled status and child `Environment` CRs:

```bash
kubectl get previewgroups
kubectl get environments -l divergedev.com/preview-group=pr-101
```

---

## Step 3: Open the Web Dashboard & Connect MCP

Port-forward `diverge-server` to access the Web UI and ConnectRPC / MCP API:

```bash
kubectl port-forward -n diverge-system svc/diverge-server 8443:8443
```

- **Web Dashboard**: Open `http://localhost:8443` in your browser.
- **CLI Health Check**:
  ```bash
  diverge doctor
  ```
- **MCP Server (for Claude Code / Cursor / VS Code)**:
  ```bash
  diverge mcp --server-url http://localhost:8443
  ```

---

## Step 4: Tear Down

```bash
kind delete cluster --name diverge-local
# or if started with make e2e-setup:
make e2e-teardown
```
