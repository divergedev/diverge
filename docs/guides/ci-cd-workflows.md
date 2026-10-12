# CI/CD Workflows (GitHub Actions & GitLab CI)

Drop-in pipeline templates that detect changed services with `diverge diff`, deploy a delta `PreviewGroup` for only the modified microservices, wait for readiness, and run end-to-end tests against the isolated preview environment.

---

## 1. `.diverge.yaml` Service Mapping

Place `.diverge.yaml` at the root of your repository so `diverge diff` maps file paths to Kubernetes services:

```yaml
version: v1
routing:
  headerKey: x-preview-env
services:
  - name: payments-api
    path: services/payments/**
    port: 8080
    database:
      mode: schema
  - name: checkout-web
    path: apps/checkout/**
    port: 3000
  - name: ledger-worker
    path: services/ledger/**
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

      - name: Detect Changed Services
        id: diff
        run: |
          CHANGED=$(diverge diff --base origin/${{ github.base_ref }} --format json)
          echo "services=$CHANGED" >> "$GITHUB_OUTPUT"

      - name: Deploy Delta PreviewGroup
        env:
          KUBECONFIG_DATA: ${{ secrets.PREVIEW_KUBECONFIG }}
        run: |
          mkdir -p ~/.kube && echo "$KUBECONFIG_DATA" | base64 -d > ~/.kube/config
          diverge preview \
            --name "pr-${{ github.event.number }}" \
            --branch "${{ github.head_ref }}" \
            --mr-id "${{ github.event.number }}" \
            --ttl 4h \
            --wait

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
    - diverge diff --base "origin/$CI_MERGE_REQUEST_TARGET_BRANCH_NAME"
    - diverge preview
        --name "mr-${CI_MERGE_REQUEST_IID}"
        --branch "${CI_COMMIT_REF_NAME}"
        --mr-id "${CI_MERGE_REQUEST_IID}"
        --ttl 4h
        --wait

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
