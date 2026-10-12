# Diverge GitLab CI/CD Component

This directory and [`templates/preview/template.yml`](../../templates/preview/template.yml) provide the official GitLab CI/CD Component for deploying and tearing down Diverge `PreviewGroup` environments on Merge Requests.

## Usage

### GitLab CI/CD Catalog Component (`templates/preview/template.yml`)

Include the component in your `.gitlab-ci.yml` file:

```yaml
include:
  - component: $CI_SERVER_FQDN/divergedev/diverge/preview@v1
    inputs:
      diverge_registry: "$CI_REGISTRY_IMAGE"
      diverge_base_url: "https://staging.example.com"
```

### Direct Component Path (`ci/gitlab/.gitlab-ci-component.yml`)

If referencing the component directly from a repository fork or legacy path:

```yaml
include:
  - component: $CI_SERVER_FQDN/divergedev/diverge/ci/gitlab/.gitlab-ci-component@main
    inputs:
      diverge_registry: "$CI_REGISTRY_IMAGE"
```

## Automatic `diverge diff` Delta Detection

When `diverge_services` is left empty (the default) and `.diverge.yaml` is present at the repository root, `diverge:preview` automatically runs:

```bash
diverge diff --base "origin/${CI_MERGE_REQUEST_TARGET_BRANCH_NAME:-main}" --output json
```

- Only modified microservices are deployed in `mode: image`.
- If no service paths were modified in the Merge Request, `diverge:preview` sets `DIVERGE_SKIPPED=true` and exits `0` without creating cluster resources.

## Inputs

| Input | Description | Default |
| :--- | :--- | :--- |
| `diverge_services` | Comma-separated list of services to deploy (leave empty to auto-detect via `diverge diff`) | `""` |
| `diverge_header_key` | HTTP header key to route preview traffic | `"x-diverge-env"` |
| `diverge_ttl` | TTL for preview environments (e.g. `4h`) | `"4h"` |
| `diverge_registry` | Container registry prefix for preview images | `""` (falls back to `$CI_REGISTRY_IMAGE`) |
| `diverge_namespace` | Default Kubernetes namespace for preview workloads | `"default"` |
| `diverge_base_url` | Optional staging/gateway URL to construct a shareable `?_diverge_env=` preview URL | `""` |
| `diverge_kubectl_version` | Version of `kubectl` to install if not present | `"v1.31.0"` |

## Exported Dotenv Variables (`preview.env`)

The `diverge:preview` job exports `preview.env` via `artifacts: reports: dotenv: preview.env`, making the following variables available to downstream E2E test jobs:

| Variable | Example | Description |
| :--- | :--- | :--- |
| `DIVERGE_PREVIEW_NAME` | `mr-42` | Name of the `PreviewGroup` resource |
| `DIVERGE_CHANGED_SERVICES_CSV` | `checkout-api` | Comma-separated list of deployed services |
| `DIVERGE_SKIPPED` | `false` | `true` when 0 services changed and deployment was skipped |
| `DIVERGE_HEADER_KEY` | `x-diverge-env` | HTTP routing header key |
| `DIVERGE_HEADER_VALUE` | `mr-42` | HTTP routing header value |
| `DIVERGE_ROUTING_HEADER` | `x-diverge-env: mr-42` | HTTP routing header string |
| `DIVERGE_ROUTING_HEADER_B64` | `eyJ4LWRpdmVyZ2UtZW52IjoibXItNDIifQ==` | Base64-encoded JSON header object (`echo "$DIVERGE_ROUTING_HEADER_B64" \| base64 -d`) for Playwright / Cypress `extraHTTPHeaders` |
| `DIVERGE_PREVIEW_URL` | `https://staging.example.com/?_diverge_env=mr-42` | Shareable browser URL with `?_diverge_env=` cookie bootstrap |

## Cluster Authentication

Provide access to your Kubernetes cluster using one of:
- `KUBECONFIG`: Path to a raw multiline kubeconfig **File** CI/CD variable (`Masked: No`).
- `KUBECONFIG_CONTENT`: Single-line base64-encoded kubeconfig content provided as a masked **Variable** (`Masked: Yes`).

## Jobs Provided

- `diverge:preview`: Runs on Merge Request events (`merge_request_event`, excluding merge trains). Fetches the target branch, detects changed services, applies the `PreviewGroup` CR, registers the GitLab `preview/mr-<iid>` environment (`on_stop: diverge:cleanup`), and exports `preview.env`.
- `diverge:cleanup`: Environment `stop` job triggered automatically when the Merge Request is merged or closed (or manually from the GitLab UI), deleting the `PreviewGroup` to free cluster resources immediately.
