# Web Dashboard & OIDC Authentication Guide

`diverge-server` includes an embedded single-page Web Dashboard (`web/`) built with React and ConnectRPC that visualizes active `PreviewGroup`s, child `Environment`s, database migration hooks, live pod logs, and service topology graphs.

---

## 1. Enabling the Web Dashboard

Enable both `server.enabled` and `server.dashboard.enabled` in your Helm values:

```yaml
# values-production.yaml
server:
  enabled: true
  replicaCount: 2
  dashboard:
    enabled: true
  cors:
    allowedOrigins: "https://diverge.example.com"
```

Install or upgrade the release:

```bash
kubectl apply -f charts/diverge/crds/
helm upgrade --install diverge charts/diverge \
  -n diverge-system --create-namespace \
  -f values-production.yaml
```

---

## 2. Authentication Modes

`diverge-server` supports a composable authentication chain:

1. **OIDC Browser Login + JWT Bearer Auth** (`--oidc-issuer-url`) — For human users via Okta, Google Workspace, GitHub/Dex, or Keycloak.
2. **Kubernetes `TokenReview` Auth** (always enabled) — For CI runners and `kubectl` service account tokens.
3. **Macaroon Capability Auth** (`DIVERGE_MACAROON_ROOT_KEY`) — For sandboxed `AgentTask` pods calling back into `diverge-server`.

---

## 3. Configuring OIDC (Okta / Google / Dex / Keycloak)

1. Create a Secret for your OIDC client secret:

```bash
kubectl create secret generic diverge-oidc-client \
  -n diverge-system \
  --from-literal=client-secret="YOUR_OIDC_CLIENT_SECRET"
```

2. Configure `server.auth` in Helm (matching `charts/diverge/values.yaml`):

```yaml
server:
  enabled: true
  dashboard:
    enabled: true
  cors:
    allowedOrigins: "https://diverge.example.com"
  auth:
    audiences:
      - diverge-api
    secureCookies: true
    oidc:
      enabled: true
      issuerUrl: https://sso.example.com/oauth2/default
      clientId: diverge-web
      redirectUrl: https://diverge.example.com/auth/callback
      allowedGroups: "platform-engineering,developers"
      clientSecretSecretRef:
        name: diverge-oidc-client
        key: client-secret
```

### Security Highlights
- **CSRF State + Sanitized Redirects**: `/auth/login` generates a cryptographic random state stored in an `HttpOnly`, `SameSite=Lax` cookie and sanitizes `return_url` to prevent open redirects.
- **Group Enforcement**: When `allowedGroups` is configured, `/auth/callback` verifies group membership before issuing the `diverge_token` session cookie.
- **Namespace-Scoped RBAC**: Every API call is checked against Kubernetes RBAC / `SubjectAccessReview` for the authenticated principal.
