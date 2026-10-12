# Database Branching & Atlas Migrations Guide

Diverge isolates database state per preview environment so schema changes and seed data in one pull request never pollute shared staging databases.

---

## 1. Database Isolation Modes & Providers

`spec.database.mode` controls how the `Environment` isolates its database (`shared`, `schema`, `snapshot`, or `fresh`), while the controller's `--database-provider` (`database.provider` in Helm) selects the backing implementation:

| Helm `database.provider` | Supported `spec.database.mode` | Provisioning Time | Best For |
| :--- | :--- | :--- | :--- |
| `noop` | `shared` | Instant (0s) | Stateless PRs with no schema or data changes |
| `schema` | `schema` | < 1s | Postgres schema-per-PR (`SET search_path TO preview_mr_42`) |
| `snapshot` | `snapshot`, `fresh` | 2–10s | Full DDL + seed data isolation inside Postgres |
| `neon` | `snapshot`, `fresh` | 1–2s | Serverless Copy-on-Write Postgres branches on [Neon](https://neon.tech) |
| `cnpg` | `fresh`, `snapshot` | 10–30s | Dedicated [CloudNativePG](https://cloudnative-pg.io) cluster per PR |
| `cockroach` | `schema`, `fresh` | 1–3s | CockroachDB multi-tenant preview databases |

---

## 2. Schema-per-Environment (`mode: schema`)

In `schema` mode, Diverge connects to your baseline Postgres instance, creates an isolated schema (`preview_<sanitized_env_name>`) and scoped credentials (`diverge_preview_<schema>`), and drops the schema automatically when the `Environment` CR is deleted.

When `spec.database.connectionRef` is omitted, `diverge-controller` automatically provisions an owned Kubernetes `Secret` named `diverge-db-env-<env>-<hash8>` containing:
- `DATABASE_URL`: Scoped connection string (`postgres://diverge_preview_<schema>:.../postgres?sslmode=disable&search_path=preview_<env>,public`).
- `DIVERGE_PREVIEW_SCHEMA`: The isolated schema name (`preview_<env>`).

The controller sets `spec.database.connectionRef` to this generated Secret, and `ServiceConfigFetcher` automatically injects `DATABASE_URL` (or `spec.serviceConfig.databaseEnvKey`) into your preview pods and migration jobs.

```yaml
apiVersion: divergedev.com/v1alpha1
kind: Environment
metadata:
  name: payments-pr-42
  namespace: staging
spec:
  serviceName: payments-api
  image: ghcr.io/acme/payments-api:pr-42
  database:
    mode: schema
    migrationJob:
      image: ghcr.io/acme/payments-migrations:pr-42
      command: ["goose", "up"]
      blocking: true
```

---

## 3. Declarative Schema Migrations with Atlas Operator

In addition to imperative migration `Job`s (`database.migrationJob`), Diverge natively integrates with the [Atlas Kubernetes Operator](https://atlasgo.io/integrations/kubernetes/operator) (`db.atlasgo.io/v1alpha1`) via `spec.database.atlas` for both versioned (`AtlasMigration`) and declarative (`AtlasSchema`) workflows.

### Option A: Versioned Migrations (`AtlasMigration`)

Reference a ConfigMap (`configMap`) or remote URL (`url`) containing SQL migration files:

```yaml
apiVersion: divergedev.com/v1alpha1
kind: Environment
metadata:
  name: payments-pr-42
  namespace: staging
spec:
  serviceName: payments-api
  image: ghcr.io/acme/payments-api:pr-42
  database:
    mode: schema
    atlas:
      enabled: true
      mode: versioned
      configMap: payments-migrations-cm
      blocking: true
```

### Option B: Declarative Schema (`AtlasSchema`)

Provide the desired SQL/HCL schema via `schema` (or a `configMap`); Atlas computes and applies the safe diff automatically against the preview database DSN:

```yaml
apiVersion: divergedev.com/v1alpha1
kind: Environment
metadata:
  name: users-pr-42
  namespace: staging
spec:
  serviceName: users-api
  image: ghcr.io/acme/users-api:pr-42
  database:
    mode: schema
    atlas:
      enabled: true
      mode: declarative
      schema: |
        CREATE TABLE users (
          id UUID PRIMARY KEY,
          email TEXT NOT NULL UNIQUE,
          created_at TIMESTAMPTZ NOT NULL DEFAULT now()
        );
```

---

## 4. Inspecting & Retrying Migration Hooks

Use the CLI or MCP tools to inspect migration Job status or retry a failed migration:

```bash
# List migration and post-deploy hooks
diverge status payments-pr-42

# Or via MCP tool:
# diverge_list_hook_jobs / diverge_retry_hook
```
