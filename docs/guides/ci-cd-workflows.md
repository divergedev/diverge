# CI/CD Workflows (GitHub Actions & GitLab CI)

Drop-in pipeline templates that detect changed services with `diverge diff`, deploy a delta `PreviewGroup` for only the modified microservices, wait for readiness, and run end-to-end tests against the isolated preview environment.

---

## 1. `.diverge.yaml` Service Mapping

Place `.diverge.yaml` at the root of your repository so `diverge diff` maps file paths to Kubernetes services:

```yaml
version: "1"
service: payments-api
services:
  payments-api:
    paths:
      - services/payments/**
    port: 8080
  checkout-web:
    paths:
      - apps/checkout/**
    port: 3000
  ledger-worker:
    paths:
      - services/ledger/**
    port: 9090
```

---

## 2. GitHub Actions Workflow (`.github/workflows/preview.yml`)

```yaml
name: Preview Environment

on:
  pull_request:
    types: [opened, synchronize, reopened, closed]

permissions:
  contents: read
  pull-requests: write
  statuses: write

jobs:
  preview:
    if: github.event.action != 'closed'
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0

      - name: Install Diverge CLI
        run: curl -fsSL https://raw.githubusercontent.com/divergedev/diverge/main/install.sh | sh

      - name: Detect Changed Services & Deploy Delta PreviewGroup
        env:
          KUBECONFIG_DATA: ${{ secrets.PREVIEW_KUBECONFIG }}
        run: |
          mkdir -p ~/.kube && echo "$KUBECONFIG_DATA" | base64 -d > ~/.kube/config
          DIFF_JSON=$(diverge diff --base "origin/${{ github.base_ref }}" --output json)
          mapfile -t SVC_ARGS < <(echo "$DIFF_JSON" | jq -r '.changedServices[]? | "--service=\(.)=ghcr.io/acme/\(.):pr-${{ github.event.number }}"')
          if [ "${#SVC_ARGS[@]}" -eq 0 ]; then
            echo "No service changes detected; skipping preview deployment."
            exit 0
          fi
          diverge preview create "pr-${{ github.event.number }}" \
            --branch "${{ github.head_ref }}" \
            --mr-id "${{ github.event.number }}" \
            --ttl 4h \
            "${SVC_ARGS[@]}"

      - name: Run Playwright E2E Against Preview
        run: |
          npx playwright test
        env:
          EXTRA_HTTP_HEADERS: '{"x-preview-env":"pr-${{ github.event.number }}"}'

  cleanup:
    if: github.event.action == 'closed'
    runs-on: ubuntu-latest
    steps:
      - name: Install Diverge CLI
        run: curl -fsSL https://raw.githubusercontent.com/divergedev/diverge/main/install.sh | sh

      - name: Delete PreviewGroup
        env:
          KUBECONFIG_DATA: ${{ secrets.PREVIEW_KUBECONFIG }}
        run: |
          mkdir -p ~/.kube && echo "$KUBECONFIG_DATA" | base64 -d > ~/.kube/config
          kubectl delete previewgroup "pr-${{ github.event.number }}" --ignore-not-found
```

---

## 3. GitLab CI Pipeline (`.gitlab-ci.yml`)

```yaml
stages:
  - preview
  - test
  - cleanup

deploy_preview:
  stage: preview
  image: ghcr.io/divergedev/diverge-cli:latest
  rules:
    - if: $CI_PIPELINE_SOURCE == "merge_request_event"
  script:
    - DIFF_JSON=$(diverge diff --base "origin/$CI_MERGE_REQUEST_TARGET_BRANCH_NAME" --output json)
    - SVC_ARGS=$(echo "$DIFF_JSON" | jq -r '.changedServices[]? | "--service=\(.)=registry.example.com/\(.):mr-'"${CI_MERGE_REQUEST_IID}"'"')
    - |
      if [ -n "$SVC_ARGS" ]; then
        diverge preview create "mr-${CI_MERGE_REQUEST_IID}" \
          --branch "${CI_COMMIT_REF_NAME}" \
          --mr-id "${CI_MERGE_REQUEST_IID}" \
          --ttl 4h \
          $SVC_ARGS
      fi

e2e_preview:
  stage: test
  rules:
    - if: $CI_PIPELINE_SOURCE == "merge_request_event"
  variables:
    DIVERGE_HEADER: "x-preview-env: mr-${CI_MERGE_REQUEST_IID}"
  script:
    - npm ci
    - npx playwright test

stop_preview:
  stage: cleanup
  image: bitnami/kubectl:latest
  rules:
    - if: $CI_PIPELINE_SOURCE == "merge_request_event"
      when: manual
  environment:
    name: preview/mr-${CI_MERGE_REQUEST_IID}
    action: stop
  script:
    - kubectl delete previewgroup "mr-${CI_MERGE_REQUEST_IID}" --ignore-not-found
```

---

## 4. Interactive PR / MR Slash Commands

In addition to CI-driven preview deployments, Diverge's webhook handlers execute interactive `/diverge` slash commands posted in GitHub Pull Request and GitLab Merge Request comments.

### Webhook Event Subscriptions
- **GitHub**: Subscribe your webhook (`/github-webhook`) to **Issue comments** (`issue_comment`) in addition to **Pull requests** (`pull_request`). For security, only comments on Pull Requests from repository `OWNER`, `MEMBER`, or `COLLABORATOR` accounts are executed.
- **GitLab**: Enable **Comments** (`Note Hook` on Merge Requests) in addition to **Merge request events** on `/gitlab-webhook`.

### PR / MR Slash Commands Reference

| Slash Command | Target Resource(s) | Description |
| :--- | :--- | :--- |
| `/diverge preview` | `Environment` | Creates or reconciles an `Environment` (`pr-<num>` / `mr-<num>`) for the current PR/MR branch. |
| `/diverge fix <objective>` | `AgentTask` | Spawns or updates an autonomous `AgentTask` (and linked preview environment) targeting the PR/MR branch with `<objective>`. |
| `/diverge extend-ttl [duration]` | `Environment`, `PreviewGroup` | Extends `spec.lifecycle.ttl` for all preview environments and preview groups matching the PR/MR (defaults to `24h` if `[duration]` is omitted, e.g., `/diverge extend-ttl 48h`). |
| `/diverge teardown` | `Environment`, `PreviewGroup` | Immediately deletes all `Environment` and `PreviewGroup` resources associated with the PR/MR. |
| `/diverge pause` | `AgentTask` | Suspends active `AgentTask` runs associated with the PR/MR (`spec.suspended = true`). |
| `/diverge resume` | `AgentTask` | Resumes paused `AgentTask` runs associated with the PR/MR (`spec.suspended = false`). |

### Examples

```text
/diverge preview
/diverge fix Resolve the flaky checkout integration test and update currency rounding
/diverge extend-ttl 48h
/diverge pause
/diverge resume
/diverge teardown
```

