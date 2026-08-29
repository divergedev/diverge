# GitLab CI/CD Integration for Diverge

This directory contains a complete GitLab CI/CD pipeline for Diverge preview environments.

## Pipeline Stages

| Stage | Job | Description |
|-------|-----|-------------|
| `build` | `build` | Build Docker image with Kaniko, push to GitLab Container Registry |
| `analyze` | `analyze` | `diverge diff` to detect changed services, `diverge route` to trace request paths |
| `preview` | `preview:deploy` | Deploy preview environment, post sticky MR comment with summary table |
| `test` | `test:preview` | Run health check against preview environment using routing header |
| `cleanup` | `cleanup` | Tear down preview environment when MR is merged or closed |

## Features

- **Change Detection**: `diverge diff --output json` identifies which services changed based on git diff + `.diverge.yaml` path mappings
- **Route Tracing**: `diverge route <service>` traces the ingress path for each changed service
- **Sticky MR Comments**: Creates/updates a single MR note with deployment summary, preview URL, and route traces (using `<!-- diverge-preview-comment -->` marker)
- **Delta Deployments**: Only modified services get new pods — unchanged services fall back to baseline via mesh routing
- **Automatic Cleanup**: Preview environments are deleted on MR merge or close

## Prerequisites

### 1. Kubernetes Cluster Access

**Option A: GitLab Kubernetes Agent (recommended)**
```yaml
variables:
  KUBE_CONTEXT: path/to/project:agent-name
before_script:
  - kubectl config use-context $KUBE_CONTEXT
```

**Option B: KUBECONFIG CI/CD variable**
Define a CI/CD variable `KUBECONFIG` of type "File" containing your cluster kubeconfig.

### 2. CI/CD Variables

| Variable | Type | Required | Description |
|----------|------|----------|-------------|
| `KUBECONFIG` | File | Yes | Kubernetes cluster credentials |
| `DIVERGE_GITLAB_TOKEN` | Variable (masked) | Yes | GitLab API token with `api` scope for MR comments |

### 3. Diverge Controller

The controller must be running in your cluster with GitLab notifier configured:

```bash
helm install diverge diverge/diverge \
  --set notifierProvider=gitlab \
  --set controller.env.DIVERGE_NOTIFIER_TOKEN=<gitlab-api-token> \
  --set controller.env.DIVERGE_WEBHOOK_SECRET=<webhook-secret>
```

Register webhooks in GitLab: **Settings → Webhooks** → URL: `https://<diverge-server>/gitlab-webhook`

## Quick Start

1. Copy `.gitlab-ci.yml` and `.diverge.yaml` to your repository root
2. Update the `DIVERGE_VERSION` variable to the latest release
3. Update the `REGISTRY` variable if not using GitLab Container Registry
4. Configure CI/CD variables (`KUBECONFIG`, `DIVERGE_GITLAB_TOKEN`)
5. Open a Merge Request — the pipeline will automatically deploy a preview

## Multi-Service (PreviewGroup)

For deploying multiple interdependent services, see the `multi-service/` directory which uses `diverge preview create` with `--service` flags.

## MR Comment Example

When the pipeline runs, it posts a comment like this on your MR:

> ## 🚀 Diverge Preview Environment
>
> | Property | Value |
> |---|---|
> | **Environment** | `preview-mr-42` |
> | **Preview URL** | https://preview-mr-42.preview.example.com |
> | **Changed Services (2)** | `backend, worker` |
> | **Deploy Mode** | `delta` (only modified services deployed) |

The comment is updated on each push — only one comment per MR.

## Comparison with GitHub Actions

Both examples provide the same capabilities:

| Feature | GitHub Actions | GitLab CI |
|---------|---------------|-----------|
| Change detection | `diverge diff` | `diverge diff` |
| Route tracing | `diverge route` | `diverge route` |
| Sticky comment | `actions/github-script` | GitLab Notes API |
| Binary caching | `actions/cache` | Not cached (fast install) |
| Cleanup trigger | `pull_request: closed` | `CI_MERGE_REQUEST_EVENT_TYPE` |
| Container build | Docker/Buildx | Kaniko |

## Related

- [GitHub Actions Example](../github-actions/)
- [Diverge for GitLab Guide](../../docs/guides/diverge-for-gitlab.md)
- [GitLab CI Component](../../ci/gitlab/) — Reusable CI component for PreviewGroups
