# GitHub Actions Integration for Diverge

This directory provides a ready-to-use GitHub Actions workflow for automating **Diverge preview environments** on Pull Requests.

---

## Overview

The [`diverge-preview.yml`](diverge-preview.yml) workflow implements a complete CI/CD preview pipeline:

1. **Detects Changed Services**: Runs `diverge diff --output json --base origin/main` to identify modified services based on your `.diverge.yaml` path mappings.
2. **Simulates Request Routing**: Runs `diverge route <service>` for each changed service to verify ingress and header propagation paths.
3. **Deploys Delta Preview**: Executes `diverge create --mr <pr_number>` to spin up an isolated preview environment containing only the changed services.
4. **Updates Pull Request**: Posts or updates a sticky comment on the PR containing the live preview URL, changed services, and routing breakdown.
5. **Automated Teardown**: Automatically cleans up the preview environment (`diverge delete`) when the PR is closed or merged.

---

## Prerequisites

### 1. Repository Secrets

Configure the following secrets in your GitHub repository (**Settings > Secrets and variables > Actions**):

- **`KUBECONFIG`**: Base64-encoded or plaintext `kubeconfig` allowing access to the target Kubernetes cluster.
  *(Alternatively, if using the Diverge ConnectRPC API server, set `DIVERGE_SERVER_URL` and `DIVERGE_TOKEN`).*

### 2. Workflow Permissions

The workflow requires write permissions to comment on Pull Requests. Ensure your workflow or repository includes:

```yaml
permissions:
  contents: read
  pull-requests: write
```

---

## Workflow Walkthrough

### 1. Git Checkout with Full History

```yaml
- name: Checkout repository
  uses: actions/checkout@v4
  with:
    fetch-depth: 0
```
> **Note**: `fetch-depth: 0` ensures the full commit history is retrieved so `diverge diff` can accurately compare the PR branch against `origin/main`.

### 2. CLI Caching & Installation

The workflow caches the `diverge` binary using `actions/cache@v4` keyed by the OS and version to minimize CI build times.

```yaml
- name: Cache Diverge CLI
  id: cache-diverge
  uses: actions/cache@v4
  with:
    path: ${{ runner.temp }}/bin/diverge
    key: diverge-cli-${{ runner.os }}-${{ env.DIVERGE_VERSION }}
```

### 3. Change Detection (`diverge diff`)

```yaml
- name: Detect changed services
  id: diff
  run: |
    DIFF_JSON=$(diverge diff --output json --base origin/main)
    echo "diff_output=${DIFF_JSON}" >> $GITHUB_OUTPUT
```

`diverge diff` inspects git changes against `origin/main` and outputs structured JSON:
```json
{
  "baseRef": "origin/main",
  "services": ["order-api"],
  "count": 1
}
```

### 4. Route Simulation (`diverge route`)

For every service listed in the diff, the workflow simulates the request route:

```yaml
- name: Trace request routes for changed services
  id: routes
  run: |
    SERVICES=$(echo '${{ steps.diff.outputs.diff_output }}' | jq -r '.services[]?')
    for svc in $SERVICES; do
      diverge route "$svc"
    done
```

### 5. Delta Deployment (`diverge create`)

```yaml
- name: Deploy preview environment
  id: deploy
  run: |
    diverge create --mr "${{ github.event.pull_request.number }}"
```

Diverge reads your `.diverge.yaml` (with `deploy.mode: delta`) and creates an ephemeral `Environment` custom resource. Only modified services are deployed; all other requests route to baseline cluster services.

### 6. Sticky PR Commenting

Using `actions/github-script@v7`, the workflow creates or edits a single comment on the PR with the deployment status and routing breakdown:

```text
🚀 Diverge Preview Environment

| Property | Value |
|---|---|
| Environment | preview-mr-42 |
| Preview URL | https://preview-mr-42.preview.example.com |
| Changed Services (1) | order-api |
| Deploy Mode | delta (only modified services deployed) |
```

### 7. Automated Cleanup on PR Close

When a PR is closed or merged (`github.event.action == 'closed'`), the `cleanup` job runs:

```yaml
- name: Delete preview environment
  run: |
    diverge delete "preview-mr-${{ github.event.pull_request.number }}"
```

---

## Quick Start

1. Place `.diverge.yaml` in the root of your repository.
2. Copy [`diverge-preview.yml`](diverge-preview.yml) into `.github/workflows/diverge-preview.yml`.
3. Add the `KUBECONFIG` secret to your GitHub repository settings.
4. Open a Pull Request to see your preview environment deploy automatically!
