# Diverge GitLab CI Component

This is a GitLab CI Component that provides reusable jobs to integrate Diverge Preview Environments into your project.

## Usage

Include this component in your `.gitlab-ci.yml` file:

```yaml
include:
  - component: $CI_SERVER_FQDN/divergedev/diverge/gitlab-ci-component@main
    inputs:
      diverge_services: "my-service"
      diverge_registry: "$CI_REGISTRY/my-group/my-project"
```

## Inputs

| Input | Description | Default |
|-------|-------------|---------|
| `diverge_services` | Comma-separated list of services to deploy | `""` |
| `diverge_header_key` | Header key to route traffic | `"x-preview-env"` |
| `diverge_ttl` | TTL for preview environments (e.g. 2h) | `"2h"` |
| `diverge_registry` | Container registry to push images to | (Required) |

## Jobs Provided

- `diverge:preview`: Runs on Merge Request events. Builds your image using Kaniko and applies a `PreviewGroup` CR.
- `diverge:cleanup`: Runs when an MR is merged or closed, automatically deleting the `PreviewGroup` to free up resources.
