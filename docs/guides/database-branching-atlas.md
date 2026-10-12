# Database Branching & Atlas Migrations Guide

Diverge isolates database state per preview environment so schema changes and seed data in one pull request never pollute shared staging databases.

---

## 1. Choosing a Database Isolation Mode

| Mode | Provisioning Time | Storage Overhead | Best For |
| :--- | :--- | :--- | :--- |
| `shared` | Instant (0s) | Zero | Stateless PRs with no schema or data changes |
| `schema` | < 1s | Minimal | Postgres schema-per-PR (`SET search_path TO preview_mr_42`) |
| `snapshot` | 2–10s | Copy-on-Write / Dump | Full DDL + seed data isolation inside Postgres |
| `neon` | 1–2s | Serverless CoW Branch | Teams using [Neon](https://neon.tech) serverless Postgres |
| `cnpg` | 10–30s | Dedicated K8s Pod | Full [CloudNativePG](https://cloudnative-pg.io) cluster per PR |
| `cockroach` | 1–3s | Logical DB | CockroachDB multi-tenant preview databases |

---

## 2. Schema-per-Environment (`mode: schema`)

In `schema` mode, Diverge connects to your baseline Postgres instance, creates an isolated schema (`preview_<sanitized_env_name>`) and scoped credentials, writes a Kubernetes `Secret` (`<env>-db-dsn`) containing `DATABASE_URL`, and drops the schema automatically when the `Environment` CR is deleted.

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
    migration:
      image: ghcr.io/acme/payments-migrations:pr-42
      command: ["goose", "up"]
      blocking: true
```

---

## 3. Declarative Schema Migrations with Atlas Operator

In addition to imperative migration `Job`s (`database.migration.command`), Diverge natively integrates with the [Atlas Kubernetes Operator](https://atlasgo.io/integrations/kubernetes/operator) (`db.atlasgo.io/v1alpha1`) for both versioned (`AtlasMigration`) and declarative (`AtlasSchema`) workflows.

### Option A: Versioned Migrations (`AtlasMigration`)

Reference a ConfigMap or Atlas Cloud directory containing SQL migration files:

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
    migration:
      engine: atlas
      atlas:
        dirConfigMapRef: payments-migrations-cm
```

### Option B: Declarative Schema (`AtlasSchema`)

Provide the desired SQL schema inline or via ConfigMap; Atlas computes and applies the safe diff automatically against the preview database DSN:

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
    migration:
      engine: atlas
      atlas:
        schemaSQL: |
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
