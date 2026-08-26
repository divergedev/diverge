# ADR: Database Migration & Post-Deploy Hooks

## Status: Accepted

## Context

When Diverge provisions isolated database schemas for preview environments, the raw schema is initially empty. Applications require their database schemas (and often initial seed data) to be present before they can start successfully. We need a flexible, native way to run database migrations against these ephemeral schemas as part of the preview environment lifecycle.

## Decision

### Why generic Job + Atlas Operator (not just one)

While the generic Job approach supports any migration tool, many users adopt declarative schema management via the Atlas Operator. Providing first-class support for Atlas CRDs (`AtlasMigration`, `AtlasSchema`) offers a significantly better developer experience for Atlas users (including native reporting and status updates), while the generic Job ensures that users of Alembic, Flyway, Prisma, etc., are fully supported.

### Why migrationJob auto-injects DATABASE_URL but postDeploy doesn't

A `migrationJob` has exactly one purpose: interacting with the newly provisioned database schema. Auto-injecting `DATABASE_URL` reduces boilerplate. `postDeploy` hooks are generic (e.g., smoke tests, cache warming, external API registration) and may not need database access; thus, they receive the standard environment variables injected into the deployment but aren't strictly treated as database clients unless configured so.

### Why blocking is default for migrations

A preview environment with an unmigrated database schema is generally useless, as the application will likely crash on startup or fail health checks. By blocking environment creation on the migration job's success, we ensure the preview environment is truly ready before marking it as such.

### Why non-blocking is default for postDeploy

Post-deploy tasks like cache warming or seed data insertion are often "nice-to-haves" rather than strict requirements for the application to serve traffic. Defaulting to non-blocking ensures that the preview environment is available to developers as quickly as possible, without being gated by potentially slow supplementary tasks.

### Why backoffLimit: 0

Database migrations, especially against empty schemas, should ideally succeed on the first attempt if the image and configuration are correct. Retrying broken migrations (e.g., syntax errors in SQL, missing credentials) is dangerous and wastes resources. Failing fast allows developers to identify and fix the issue immediately.

### Why Secret-based DSN injection

The `DATABASE_URL` contains sensitive credentials for connecting to the database. Injecting it directly as an environment variable in the Pod spec would expose it in plaintext within the Kubernetes API. Using a Secret ensures the credentials are encrypted at rest (if configured in the cluster) and accessed securely.

### Why ttlSecondsAfterFinished: 300

Keeping completed or failed Jobs around indefinitely clutters the cluster (poor hygiene) and can degrade apiserver performance. However, immediate deletion removes the ability to inspect logs for failed migrations. A 5-minute TTL strikes a balance, providing enough time for automated systems or developers to retrieve logs while maintaining cluster cleanliness.

## Consequences

This design enables a seamless developer experience for both traditional migration tools and modern declarative operators. It does not attempt to solve complex multi-stage rollouts, leaving those to dedicated continuous delivery tools.
