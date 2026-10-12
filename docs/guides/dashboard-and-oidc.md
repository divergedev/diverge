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
    allowedOrigins:
      - https://diverge.example.com
```

Install or upgrade the release:

```bash
helm upgrade --install diverge charts/diverge \
  -n diverge-system --create-namespace \
  -f values-production.yaml
```

---

## 2. Authentication Modes

`diverge-server` supports a composable authentication chain:

1. **OIDC Browser Login + JWT Bearer Auth** (`--oidc-issuer`) — For human users via Okta, Google Workspace, GitHub/Dex, or Keycloak.
2. **Kubernetes `TokenReview` Auth** (`--enable-k8s-token-auth=true`) — For CI runners and `kubectl` service account tokens.
3. **Macaroon Capability Auth** (`DIVERGE_MACAROON_ROOT_KEY`) — For sandboxed `AgentTask` pods calling back into `diverge-server`.

---

## 3. Configuring OIDC (Okta / Google / Dex / Keycloak)

1. Create Secrets for your OIDC client secret and 32-byte session encryption key:

```bash
kubectl create secret generic diverge-oidc-client \
  -n diverge-system \
  --from-literal=client-secret="YOUR_OIDC_CLIENT_SECRET"

kubectl create secret generic diverge-session-key \
  -n diverge-system \
  --from-literal=session-key="$(openssl rand -hex 32)"
```

2. Configure `server.auth` in Helm:

```yaml
server:
  enabled: true
  dashboard:
    enabled: true
  auth:
    audiences:
      - diverge-api
    enableK8sTokenAuth: true
    secureCookies: true
    oidc:
      enabled: true
      issuer: https://sso.example.com/oauth2/default
      clientID: diverge-web
      redirectURI: https://diverge.example.com/auth/callback
      allowedGroups:
        - platform-engineering
        - developers
      clientSecretRef:
        name: diverge-oidc-client
        key: client-secret
    session:
      secretRef:
        name: diverge-session-key
        key: session-key
```

### Security Highlights
- **PKCE + State Validation**: `/auth/login` generates a cryptographic state and PKCE verifier encrypted with AES-256-GCM using the session secret.
- **Group Enforcement**: When `allowedGroups` is configured, `/auth/callback` verifies group membership before issuing the `HttpOnly`, `Secure`, `SameSite=Lax` session cookie.
- **Namespace-Scoped RBAC**: When `server.rbac.enforceUserRBAC=true`, every API call is checked against Kubernetes `SubjectAccessReview` for the authenticated user and groups.
