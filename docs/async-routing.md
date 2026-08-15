# Async Routing in Diverge

Diverge provides first-class support for event-driven architectures and async routing. By provisioning unique, per-preview message queues, topics, or task queues, developers can test asynchronous workloads in complete isolation without interfering with the baseline environment or other developers.

## 1. Overview

Testing asynchronous services (e.g., workers consuming Kafka messages or Temporal workflows) in a shared cluster is notoriously difficult. If multiple environments share the same topic or task queue, messages are processed nondeterministically by whichever consumer polls first.

Diverge solves this by:
1. Creating **ephemeral async targets** (e.g., dedicated Kafka topics or Temporal task queues) for each preview environment.
2. **Injecting environment variables** into your preview pods so they automatically consume from their dedicated targets.
3. Providing **subdomain routing** to expose HTTP endpoints for services interacting with these isolated asynchronous workflows.

## 2. Subdomain Routing Setup

For complete end-to-end testing, frontend applications and webhooks often need to talk to your preview backend without passing special HTTP headers (like `x-preview-env`).

By setting `mode: subdomain` in the routing configuration, Diverge configures the underlying Gateway API to route traffic based on the hostname rather than HTTP headers. This creates browser-accessible previews at `<env-name>.<baseDomain>`.

**Example:**
```yaml
apiVersion: diverge.io/v1alpha1
kind: PreviewGroup
metadata:
  name: mr-101
spec:
  routing:
    mode: subdomain
    baseDomain: preview.app.dev
```
With this setup, the preview environment is accessible directly via `https://mr-101.preview.app.dev`. Ensure you have a wildcard DNS record (`*.preview.app.dev`) pointing to your Gateway ingress.

## 3. Temporal Integration

The Temporal provisioner automatically generates isolated task queue names for preview environments, ensuring preview workers only pick up workflows intended for them.

**Key Features:**
- Generates a unique task queue: `<target>-<envName>`
- Injects `TEMPORAL_TASK_QUEUE` environment variable (customizable via `envVarMapping`)

**Controller Configuration:**
- `--async-provider=temporal`
- `--temporal-namespace`: Set the Temporal namespace to use for the provisioner.

## 4. Kafka / AutoMQ Integration

The Kafka provisioner uses the Kafka AdminClient (compatible with AutoMQ, Kafka, and Redpanda) to dynamically provision isolated topics for each preview environment and cleans them up when the environment is torn down.

**Key Features:**
- Creates unique topics and consumer groups.
- Automatically injects `KAFKA_TOPIC`, `KAFKA_CONSUMER_GROUP`, and `KAFKA_BROKERS` into preview pods.

**Controller Configuration:**
- `--async-provider=kafka`
- `--kafka-brokers`: Comma-separated list of Kafka brokers.
- `--kafka-partitions`: Number of partitions for newly created topics.
- `--kafka-replication-factor`: Replication factor for new topics.

## 5. Custom Providers (Webhooks)

If your architecture uses a different message broker or requires complex custom infrastructure (like AWS SQS or GCP Pub/Sub), you can use the `webhook` async provisioner.

Diverge will issue an HTTP POST request to your configured webhook when an environment spins up, allowing your custom service to provision the queue and return the environment variables to inject.

## 6. Configuration Reference

### CLI Flags (Controller)
* `--async-provider`: The async provisioner to use (`noop`, `temporal`, `kafka`, `webhook`).
* `--temporal-namespace`: Namespace for Temporal workflows.
* `--kafka-brokers`: Broker list for the Kafka AdminClient.
* `--kafka-partitions`: Default partitions for new Kafka topics.
* `--kafka-replication-factor`: Default replication factor for new Kafka topics.

### Environment CRD (`EnvironmentRouting`)
* `mode`: Defines how HTTP traffic is routed. Options: `header`, `subdomain`, `namespace`.
* `baseDomain`: Required when mode is `subdomain`.
* `asyncRoutes`: List of asynchronous targets to provision.

**AsyncRouteSpec:**
* `protocol`: The protocol/provider to use (`temporal`, `kafka`).
* `target`: The baseline resource name (e.g., base topic name or task queue name).
* `envVarMapping`: (Optional) Map of environment variable names to templates. Diverge populates default variables if omitted.

### Slim Builds (Build Tags)
Diverge offers slim binaries for deployments that don't need all providers. Use the `no_temporal` or `no_kafka` Go build tags to exclude these SDKs, significantly reducing binary size.

## 7. Examples

### Complete PreviewGroup with Subdomain and Async Routing

```yaml
apiVersion: diverge.io/v1alpha1
kind: PreviewGroup
metadata:
  name: mr-42
spec:
  source:
    provider: gitlab
    project: myorg/backend
    branch: feat/async-workers
  routing:
    mode: subdomain
    baseDomain: preview.app.dev
  services:
    - name: api-server
      image: registry.example.com/api-server:mr-42
    - name: payment-worker
      image: registry.example.com/payment-worker:mr-42
```

### Environment CR with Kafka and Temporal

When the controller processes the `PreviewGroup`, it generates `Environment` CRs that define the specific asynchronous routing targets:

```yaml
apiVersion: diverge.io/v1alpha1
kind: Environment
metadata:
  name: mr-42
spec:
  deploy:
    mode: full
  routing:
    mode: subdomain
    baseDomain: preview.app.dev
    asyncRoutes:
      - protocol: kafka
        target: payments-events
        # Diverge injects KAFKA_TOPIC, KAFKA_CONSUMER_GROUP, and KAFKA_BROKERS automatically
      - protocol: temporal
        target: payments-workflows
        envVarMapping:
          CUSTOM_TASK_QUEUE: "{{ .ResolvedTarget }}"
```
