# ConnectRPC API Server

Diverge v0.6.0 introduces an optional ConnectRPC API server, which acts as a stateless Kubernetes CRD facade. It allows programmatic interaction with Diverge custom resources (`Environment`, `PreviewGroup`, etc.) via HTTP/1.1 and HTTP/2, enabling browser-native client support and simple curl commands.

## Architecture

The Diverge server does not use its own database. It serves as a stateless proxy over the Kubernetes API, meaning all state is stored directly in Kubernetes Custom Resources. The server translates ConnectRPC/gRPC requests into Kubernetes API operations using the `controller-runtime` client, respecting native Kubernetes paradigms.

## Helm Installation

The API server is disabled by default. To enable it during deployment, set `server.enabled: true` in your `values.yaml` or via the Helm CLI:

```bash
helm upgrade --install diverge diverge/diverge \
  --namespace diverge-system \
  --create-namespace \
  --set server.enabled=true \
  --version v0.6.0
```

## Security Features

### Authentication

The API server uses Kubernetes `TokenReview` for authentication.
It supports standard OIDC JWTs (JSON Web Tokens) or Kubernetes ServiceAccount tokens.

To authenticate requests, pass your token in the `Authorization` header as a Bearer token:
```
Authorization: Bearer <your-jwt-or-sa-token>
```

Tokens are cached via an LRU cache to reduce load on the Kubernetes API server (configurable via `server.auth.tokenCacheTTL`).

### Authorization

Authorization is implemented using Kubernetes `SubjectAccessReview` (SAR) to enforce Namespace-scoped RBAC. When a user attempts to interact with an `Environment` or stream logs, the API server verifies that the authenticated user has the necessary RBAC permissions for the target namespace and resource in the cluster.

You can configure the server to allow cluster-wide pod access for logs or restrict it to specific target namespaces via the `server.rbac.clusterWidePodAccess` and `server.rbac.targetNamespaces` Helm values.

### CORS Configuration

For browser and SPA clients, the server supports Cross-Origin Resource Sharing (CORS). By default, it allows all origins (`*`). In production, this should be restricted to your domain:

```yaml
server:
  cors:
    allowedOrigins: "https://your-dashboard.com,https://admin.your-dashboard.com"
    maxAge: 86400
```

### Error Sanitization

The server ensures that sensitive Kubernetes API error details are sanitized before returning them to clients. Raw errors are logged server-side for debugging, but clients receive safe, standardized ConnectRPC error codes (e.g., `CodeNotFound`, `CodeAlreadyExists`, `CodePermissionDenied`).

### Audit Logging

All authentication attempts (successes/failures), authorization denials, and resource mutations are recorded via structured audit logging. These JSON-formatted events are sent to standard output by the `audit` component and can be ingested by log aggregation tools for security monitoring. See the [Observability Guide](observability.md) for details.

## API Patterns

### Pagination

List endpoints support cursor-based pagination using `page_size` and `page_token`.

When listing resources, specify `page_size` to limit the number of results. If more results exist, the response will include a `next_page_token`. Pass this token in subsequent requests to retrieve the next page of results. Note that pagination tokens can expire; if a `CodeAborted` error is returned due to token expiration, restart the listing from the beginning.

### Optimistic Concurrency

To prevent race conditions during updates or deletes, the API server supports Kubernetes-style optimistic concurrency via the `resource_version` field.

When fetching a resource, its `resource_version` is included. When updating, provide the same `resource_version`. If the resource has been modified by another client in the meantime, the API server will return a `CodeAborted` error, indicating a conflict. You should then re-fetch the latest resource and try again.

## Usage Examples

Because the server speaks ConnectRPC, you can interact with it using standard tools like `curl`, native browser `fetch`, or any gRPC client.

### Listing Environments with curl

```bash
curl -X POST https://diverge.yourdomain.com/diverge.v1alpha1.EnvironmentService/ListEnvironments \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $YOUR_TOKEN" \
  -d '{"namespace": "default", "page_size": 10}'
```

### Fetching a Specific Environment

```bash
curl -X POST https://diverge.yourdomain.com/diverge.v1alpha1.EnvironmentService/GetEnvironment \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $YOUR_TOKEN" \
  -d '{"namespace": "default", "name": "preview-mr-1"}'
```
