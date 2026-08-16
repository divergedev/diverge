# Diverge for GitLab

Complete guide to using Diverge with GitLab — from setup to production.

## Prerequisites
- Kubernetes cluster with Diverge installed
- GitLab (SaaS or Self-Hosted)
- Container registry (GitLab Container Registry recommended)

## 1. Install Diverge
Install the Diverge controller using Helm, passing in your GitLab-specific values.
```bash
helm repo add diverge https://divergedev.github.io/diverge
helm repo update
helm install diverge diverge/diverge \
  --namespace diverge-system \
  --create-namespace \
  -f gitlab-values.yaml \
  --version v0.4.0
```

## 2. Configure GitLab Webhook
To enable automatic environment creation, configure a webhook in your GitLab project:
1. Go to your GitLab Project -> **Settings** -> **Webhooks**.
2. URL: `https://diverge.yourdomain.com/gitlab-webhook` (replace with your Diverge ingress endpoint).
3. Secret Token: Your configured webhook secret token.
4. Trigger events: Check **Merge request events**.
5. Click **Add webhook**.

For **PreviewGroups** support (multi-service environments), add a second webhook pointing to:
- URL: `https://diverge.yourdomain.com/gitlab-previewgroup-webhook`

*Note: The webhook endpoint must be accessible from your GitLab instance.*

## 3. Configure GitLab CI/CD Variables
If you use Diverge for deployment and registry access, add these variables to your GitLab CI/CD Settings (`Settings` > `CI/CD` > `Variables`):
- `DIVERGE_GITLAB_TOKEN`: GitLab personal access token or project access token (requires `api` scope).
- `DIVERGE_GITLAB_URL`: GitLab instance URL (e.g., `https://gitlab.example.com`). Required for self-hosted instances.

## 4. Helm Values for GitLab
To configure Diverge for GitLab integration, apply the following `values.yaml` during installation:

```yaml
notifier:
  provider: gitlab
  gitlab:
    url: https://gitlab.example.com  # omit for gitlab.com
    token:
      secretName: diverge-gitlab-token
      key: token
webhook:
  gitlab:
    secretToken:
      secretName: diverge-webhook-secret
      key: token
```

Create the necessary Kubernetes secrets before installing:
```bash
kubectl create secret generic diverge-gitlab-token \
  --namespace diverge-system \
  --from-literal=token=YOUR_GITLAB_TOKEN

kubectl create secret generic diverge-webhook-secret \
  --namespace diverge-system \
  --from-literal=token=YOUR_WEBHOOK_SECRET
```

## 5. Single Service Setup
For single-service repositories, the `Environment` CRD is deployed when a Merge Request webhook event is received. Ensure your `.diverge.yaml` maps the repository correctly:
```yaml
version: v1
services:
  frontend:
    path: apps/web/**
```
When an MR is opened or updated, Diverge creates an `Environment` matching the MR ID and routes header-based traffic specifically to the updated service.

## 6. Multi-Service Setup (PreviewGroups)
For monorepos with multiple inter-dependent services, the `PreviewGroup` CRD ensures all services build and deploy together for a specific MR.

When the PreviewGroup webhook is triggered:
- Diverge provisions a cohesive environment for the MR.
- MR comments are posted to summarize the collective state and provide the routing header (e.g., `x-preview-env: 123`).
- Teardown of the PreviewGroup happens automatically when the MR is closed or merged.

## 7. GitLab CI/CD Pipeline
You can integrate Diverge into your GitLab CI pipelines to build your container images and push them to the registry before environments are provisioned.

Example `.gitlab-ci.yml`:
```yaml
stages:
  - build

build-preview:
  stage: build
  image: docker:24.0.5
  services:
    - docker:24.0.5-dind
  script:
    - docker login -u $CI_REGISTRY_USER -p $CI_REGISTRY_PASSWORD $CI_REGISTRY
    - IMAGE_TAG=${CI_COMMIT_SHA:0:12}
    - docker build -t $CI_REGISTRY_IMAGE:$IMAGE_TAG .
    - docker push $CI_REGISTRY_IMAGE:$IMAGE_TAG
  rules:
    - if: $CI_PIPELINE_SOURCE == "merge_request_event"
```
Refer to the `examples/gitlab-ci/` directory for full examples.

## 8. Self-Hosted GitLab
If you are running a self-hosted GitLab instance, ensure the following are configured:
- **Custom URL configuration**: Set `notifier.gitlab.url` in your Helm values to your instance URL.
- **Internal CA certificates**: If your instance uses self-signed certs, mount your CA bundle into the controller pod.
- **Private registry authentication**: Ensure Diverge has the correct image pull secrets for your internal container registry.
- **Network considerations**: The webhook delivery must be able to reach your Kubernetes cluster from the GitLab instance. You may need to configure local network access in GitLab's Admin Area (`Settings` > `Network` > `Outbound requests`).

## 9. GitLab vs GitHub Feature Parity
| Feature | GitHub | GitLab |
|---------|--------|--------|
| Environment MR comments | ✅ | ✅ |
| PreviewGroup MR comments | ✅ | ✅ |
| Webhook signature validation | ✅ HMAC-SHA256 | ✅ Token |
| CI/CD integration | GitHub Actions | GitLab CI/CD |
| Container registry | ghcr.io | registry.gitlab.com |

## 10. Troubleshooting
- **Webhook 404** → Check that your webhook URL endpoint is correct and accessible.
- **No MR comments** → Verify your `DIVERGE_GITLAB_TOKEN` permissions; it requires the `api` scope to post notes.
- **Self-hosted cert errors** → You likely need to mount your custom CA bundle in the controller.
