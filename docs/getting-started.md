# Getting Started with Diverge

This guide will walk you through the process of setting up Diverge in your Kubernetes cluster, configuring your repository, and spinning up your first preview environment.

## Prerequisites

Before installing Diverge, ensure you have the following prerequisites installed and configured:

- **Go 1.26+**: Required if building from source (use Nix).
- **Nix**: Used for managing the development environment (`nix develop`).
- **kubectl**: For interacting with your Kubernetes cluster.
- **kind** (or another Kubernetes cluster): To run Diverge locally or in a test environment.
- **Helm v3**: For installing the Diverge chart.
- **Istio**: Required for header-based routing.
- **Argo CD** (optional but recommended): For declarative GitOps deployments of your microservices.

## Install Diverge

The easiest way to install Diverge is via Helm.

```bash
# Add the Diverge Helm repository (placeholder URL)
helm repo add diverge https://charts.divergedev.io
helm repo update

# Install the Diverge operator (v0.1.0 Release)
helm install diverge diverge/diverge --version v0.1.0 --namespace diverge-system --create-namespace
```

Alternatively, to install from source for development:
```bash
make install
make run
```

## Try the Bank Demo
For a complete hands-on demo with database schema isolation, see the [bank-demo](https://github.com/divergedev/demo).

## Configure Your Repo

Diverge relies on a configuration file inside your application repository to understand how your project is structured.

Create a `.diverge.yaml` file in the root of your repository mapping source paths to your services:

```yaml
version: v1
services:
  frontend:
    path: apps/web/**
  backend:
    path: apps/api/**
```

See the [Configuration Reference](configuration.md) for a full breakdown of `.diverge.yaml`.

## Set Up Webhook

To enable automatic environment creation when a Merge Request (MR) is opened, configure a webhook in your Version Control System (VCS).

### GitLab Example
1. Go to your GitLab Project -> **Settings** -> **Webhooks**.
2. Set the URL to your Diverge ingress endpoint (e.g., `https://diverge.yourdomain.com/webhook/gitlab`).
3. Check **Merge request events**.
4. Click **Add webhook**.

*Note: The Diverge webhook endpoint needs to be externally accessible from your VCS.*

## Create Your First Environment

If you want to manually test Diverge without a webhook event, you can apply an `Environment` Custom Resource (CR) directly.

Create a file named `sample-env.yaml`:

```yaml
apiVersion: diverge.io/v1alpha1
kind: Environment
metadata:
  name: preview-mr-1
  namespace: default
spec:
  source:
    provider: gitlab
    project: my-org/my-app
    mr: 1
    branch: feature/new-ui
  deploy:
    mode: delta
    changedServices:
      - frontend
    baselineRef: staging
  routing:
    mode: header
    headerKey: x-diverge-env
    headerValue: mr-1
  database:
    mode: schema
    migrationJob:
      image: registry.gitlab.com/my-org/my-app/migrate:latest
      args: ["up"]
      timeoutSeconds: 120
  serviceConfig:
    serviceName: frontend
    port: 3000
    image: registry.gitlab.com/my-org/my-app/frontend:mr-1
    pathPrefix: /api
  lifecycle:
    ttl: 24h
```

Apply it to the cluster:
```bash
kubectl apply -f sample-env.yaml
```

## Verify

Check the status of your newly created environment:

```bash
kubectl get environments preview-mr-1 -o wide
```

Watch for the phase to change from `Pending` -> `Deploying` -> `Running`.

Once running, you can test the routing by passing the configured HTTP header:

```bash
curl -H "x-diverge-env: mr-1" https://staging.yourdomain.com
```
You should see the response from your new `feature/new-ui` branch, while normal requests (without the header) will continue to hit the baseline staging environment.

## Clean Up

To tear down the preview environment manually:

```bash
kubectl delete environment preview-mr-1
```

The Diverge controller will intercept the deletion, run the teardown logic (cleaning up Istio rules, database schemas, etc.), and remove the environment.

## Exporting Environment Variables

If you need to connect your local development setup to the baseline environment's dependencies, you can use the `env export` command to fetch the environment variables from the baseline pod:

```bash
diverge env export --service frontend --format dotenv > .env.preview
```

This exports the environment variables in a `.env` format, ready to be used by your local development server. Supported formats include `dotenv`, `json`, and `shell`.
