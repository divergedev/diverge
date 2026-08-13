# Diverge GitLab CI Component

This is a GitLab CI Component that provides reusable jobs to integrate Diverge Preview Environments into your project.

## Usage

Include this component in your `.gitlab-ci.yml` file:

```yaml
include:
  - component: $CI_SERVER_FQDN/divergedev/diverge/ci/gitlab/.gitlab-ci-component@main
    inputs:
      diverge_services: "my-service"
      diverge_registry: "$CI_REGISTRY/my-group/my-project"
```

> **Note**: If you are publishing this component to the GitLab CI/CD catalog, users will need to reference the actual file path `ci/gitlab/.gitlab-ci-component.yml` in their configuration depending on how it's published.

## Inputs

| Input | Description | Default |
|-------|-------------|---------|
| `diverge_services` | Comma-separated list of services to deploy | `""` |
| `diverge_header_key` | Header key to route traffic | `"x-preview-env"` |
| `diverge_ttl` | TTL for preview environments (e.g. 2h) | `"2h"` |
| `diverge_registry` | Container registry to push images to | (Required) |
| `diverge_kubectl_version` | Version of kubectl to install | `"v1.30.4"` |

## Cluster Authentication

The component requires access to your Kubernetes cluster. You must provide either:
- `KUBECONFIG`: Path to a kubeconfig file.
- `KUBECONFIG_CONTENT`: Base64 encoded kubeconfig content provided as a CI/CD variable.

## Jobs Provided

- `diverge:preview`: Runs on Merge Request events. Builds your image using Kaniko and applies a `PreviewGroup` CR.
- `diverge:cleanup`: Runs when an MR is merged or closed, automatically deleting the `PreviewGroup` to free up resources.
