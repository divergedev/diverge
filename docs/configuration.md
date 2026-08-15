# Configuration Reference

This document provides a comprehensive reference for configuring Diverge, including the `.diverge.yaml` repository file, the `Environment` CRD, Helm values, and environment variables.

## `.diverge.yaml` Reference

The `.diverge.yaml` file is placed in the root of your application repository. It tells the Changeset Detector how to map modified code paths to deployable services.

| Field | Type | Default | Description |
|---|---|---|---|
| `version` | string | `v1` | The schema version of the configuration file. |
| `services` | map[string]Service | `{}` | A map where the key is the service name and the value contains the configuration for that service. |
| `services.<name>.path` | string | `""` | A glob pattern defining which file paths trigger a redeployment of this service. |

### Example

```yaml
version: v1
services:
  auth-service:
    path: services/auth/**
  payment-service:
    path: services/payment/**
```

## Environment CRD Reference

The `Environment` Custom Resource represents a single preview environment in Kubernetes.

### `EnvironmentSpec`

| Field | Description |
|---|---|
| `source.provider` | The VCS provider (e.g., `gitlab`, `github`). |
| `source.project` | The path or ID of the project in the VCS. |
| `source.mr` | The Merge Request / Pull Request ID. |
| `source.branch` | The source branch name. |
| `deploy.mode` | Deployment strategy: `delta` (only changed services) or `full` (all services). |
| `deploy.changedServices` | List of service names detected as changed by the Changeset Detector. |
| `deploy.baselineRef` | The reference environment to fall back to (e.g., `staging`). |
| `routing.mode` | The routing strategy: `header`, `namespace`, or `subdomain`. |
| `routing.headerKey` | The HTTP header key used for routing (e.g., `x-diverge-env`). |
| `routing.headerValue` | The value of the HTTP header indicating this specific environment. |
| `routing.externalUrl` | The generated external URL for the environment (populated dynamically). |
| `database.mode` | Database provisioning strategy: `shared`, `schema`, `snapshot`, `fresh`. |
| `database.connectionRef` | Reference to a Secret containing the database connection string. |
| `database.seedSource` | Path or reference to database seeding scripts. |
| `database.migrationJob` | Configuration for a Kubernetes Job to run migrations against the provisioned DB. |
| `lifecycle.ttl` | Time-to-Live duration string (e.g., `72h`). Environment is deleted after this duration. |
| `lifecycle.cleanupOnMerge` | Boolean. If true, the environment is destroyed when the MR is merged. |
| `serviceConfig` | Configuration for a single preview pod in multi-repo mode. |

### `MigrationJobSpec`

| Field | Description |
|---|---|
| `image` | **Required**. Container image to use for the migration job. |
| `args` | Command arguments to run in the container. |
| `envFrom` | List of Secret references to inject as environment variables. |
| `timeoutSeconds` | Timeout for the migration job. Default is 120. |

### `ServicePreviewConfig`

| Field | Description |
|---|---|
| `serviceName` | **Required**. The Kubernetes Service name to shadow. |
| `namespace` | The namespace where the baseline service runs. |
| `port` | **Required**. The container port the service listens on. |
| `image` | **Required**. The container image for the preview pod. |
| `imagePullPolicy` | Overrides the container image pull policy. Default is `IfNotPresent`. |
| `parentRef` | The Gateway API parentRef name. Default is `diverge-gateway`. |
| `headerKey` | Overrides the routing header key. Default is `x-diverge-env`. |
| `pathPrefix` | Scopes the preview HTTPRoute to a specific path prefix. |
| `databaseEnvKey` | The environment variable name for the database connection URL. Default is `DATABASE_URL`. |
| `env` | Additional environment variables for the preview container (list of EnvVar). |

### `EnvironmentStatus`

| Field | Description |
|---|---|
| `phase` | Current lifecycle phase: `Pending`, `Deploying`, `Running`, `Failed`, `Terminating`. |
| `url` | The primary accessible URL for the environment. |
| `services` | List of services currently actively deployed in this environment. |
| `databaseStatus` | Status of the database provisioning step. |
| `createdAt` | Timestamp when the environment was created. |
| `expiresAt` | Timestamp when the environment will be deleted (if TTL is set). |

## MR Label Overrides

Diverge can read labels applied to a Merge Request and override the default deployment configurations. This allows developers to request specific infrastructure directly from GitLab/GitHub.

Examples of label mappings:
- `diverge:db-snapshot` -> Overrides `database.mode` to `snapshot`.
- `diverge:deploy-full` -> Overrides `deploy.mode` to `full`.
- `diverge:ttl-never` -> Removes the TTL, preventing automatic expiry.

*(Note: Label mapping configuration is typically defined in the controller's Helm values).*

## Helm Chart Values

When deploying Diverge via Helm, you can customize its behavior using `values.yaml`.

Key parameters:

| Key | Default | Description |
|---|---|---|
| `webhook.enabled` | `true` | Enable the VCS webhook listener. |
| `webhook.secretName` | `""` | K8s Secret containing the webhook verification token. |
| `controller.replicaCount` | `1` | Number of controller replicas. |
| `proxy.enabled` | `true` | Enable the Diverge Proxy for advanced routing. |
| `proxy.replicaCount` | `1` | Number of proxy replicas. |
| `routing.defaultMode` | `header` | Default routing mode if not specified. |
| `database.defaultMode` | `shared` | Default database mode if not specified. |

## CLI Configuration

Diverge can be configured via CLI flags in the controller `main.go`:

| Flag | Default | Description |
|---|---|---|
| `--deploy-provider` | `noop` | Deployment provider (`direct`, `argocd`, `knative`, `noop`). |
| `--routing-provider` | `gateway` | Routing provider (`gateway`, `istio`, `composite`, `noop`). |
| `--database-provider` | `none` | Database provider (`schema`, `none`). |
| `--notifier-provider` | `noop` | Notification provider (`github`, `gitlab`, `noop`). |

### Provider-Specific Flags
Because providers self-register, some flags are tied to specific providers (registered in their `*_register.go` files):
- **Direct Deployer**: `--manifest-source-type` (`configmap`, `url`, `serviceconfig`).
- **ArgoCD Deployer**: `--argo-namespace`, `--argo-repo-url`.
- **GitLab Notifier**: `--gitlab-token`, `--gitlab-url`.

## CLI Commands

### Env Export
The `diverge env export` command allows you to export environment variables from a baseline pod. This is useful for running your local preview environments seamlessly.

```bash
diverge env export --service payments --format dotenv > .env.preview
```
Supported formats: `dotenv`, `json`, `shell`.

## Environment Variables

The Diverge controller can be configured via environment variables (usually injected via the Helm chart):

- `DIVERGE_WEBHOOK_SECRET`: The secret token used to validate incoming webhooks from GitLab/GitHub.
- `DIVERGE_LOG_LEVEL`: Logging verbosity (`debug`, `info`, `warn`, `error`).
- `DIVERGE_DEFAULT_BASELINE`: The default baseline environment name (e.g., `staging`).
