# SSO Setup Guide

Diverge supports Single Sign-On (SSO) via any OpenID Connect (OIDC) provider.
This guide covers setup for popular identity providers.

## How It Works

```
Browser → /auth/login → OIDC Provider → /auth/callback → Session Cookie → Dashboard
```

When SSO is enabled, the login page shows a "Sign in with [Provider]" button.
Clicking it redirects to your identity provider, which authenticates the user
and redirects back with an authorization code. Diverge exchanges the code for
an ID token, verifies it, and creates a session cookie.

**ServiceAccount token login continues to work** alongside SSO — the two
authentication methods coexist.

---

## Google

### 1. Create OAuth Client

1. Go to [Google Cloud Console → APIs & Services → Credentials](https://console.cloud.google.com/apis/credentials)
2. Click **Create Credentials → OAuth client ID**
3. Application type: **Web application**
4. Name: `Diverge Dashboard`
5. Authorized redirect URIs: `https://diverge.example.com/auth/callback`
6. Copy the **Client ID** and **Client Secret**

### 2. Create Kubernetes Secret

```bash
kubectl create secret generic diverge-oidc-secret \
  --namespace diverge \
  --from-literal=clientSecret=GOCSPX-your-secret-here
```

### 3. Configure Helm Values

```yaml
server:
  auth:
    oidc:
      enabled: true
      issuerUrl: "https://accounts.google.com"
      clientId: "123456789.apps.googleusercontent.com"
      clientSecretSecretRef:
        name: "diverge-oidc-secret"
        key: "clientSecret"
      redirectUrl: "https://diverge.example.com/auth/callback"
      providerName: "Google"
      scopes: "openid,profile,email"
```

### 4. Upgrade

```bash
helm upgrade diverge diverge/diverge -f values.yaml
```

The login page will now show a **"Sign in with Google"** button.

---

## Okta

### 1. Create Application

1. In the Okta Admin Console, go to **Applications → Create App Integration**
2. Sign-in method: **OIDC - OpenID Connect**
3. Application type: **Web Application**
4. Grant type: **Authorization Code**
5. Sign-in redirect URI: `https://diverge.example.com/auth/callback`
6. Sign-out redirect URI: `https://diverge.example.com/login`
7. Assignments: Assign to desired groups
8. Copy the **Client ID** and **Client Secret**

### 2. Create Kubernetes Secret

```bash
kubectl create secret generic diverge-oidc-secret \
  --namespace diverge \
  --from-literal=clientSecret=your-okta-client-secret
```

### 3. Configure Helm Values

```yaml
server:
  auth:
    oidc:
      enabled: true
      issuerUrl: "https://dev-12345.okta.com/oauth2/default"
      clientId: "0oa1234567890"
      clientSecretSecretRef:
        name: "diverge-oidc-secret"
        key: "clientSecret"
      redirectUrl: "https://diverge.example.com/auth/callback"
      providerName: "Okta"
      # Optional: restrict to specific groups
      allowedGroups: "platform-team,developers"
```

---

## Zitadel

### 1. Create Application

1. In the Zitadel Console, go to **Projects → Your Project → Applications → New**
2. Application type: **Web**
3. Authentication method: **PKCE** (or **Code**)
4. Redirect URI: `https://diverge.example.com/auth/callback`
5. Post-logout URI: `https://diverge.example.com/login`
6. Copy the **Client ID**

### 2. Configure Project Roles

1. Go to **Projects → Your Project → Roles**
2. Create roles for Diverge access (e.g., `developer`, `admin`, `viewer`)
3. Go to **Projects → Your Project → Settings** and enable **Assert Roles on Authentication**
4. Assign roles to users in **Users → Authorizations**

> [!IMPORTANT]
> Zitadel returns roles as a **map** in the JWT claim
> `urn:zitadel:iam:org:project:roles`, not as a string array. Diverge v0.8.1+
> handles this format natively by extracting the map keys as role names.

### 3. Create Kubernetes Secret

```bash
kubectl create secret generic diverge-oidc-secret \
  --namespace diverge \
  --from-literal=clientSecret=your-zitadel-client-secret
```

### 4. Configure Helm Values

```yaml
server:
  auth:
    oidc:
      enabled: true
      issuerUrl: "https://your-instance.zitadel.cloud"
      clientId: "your-diverge-client-id"
      clientSecretSecretRef:
        name: "diverge-oidc-secret"
        key: "clientSecret"
      redirectUrl: "https://diverge.example.com/auth/callback"
      providerName: "Zitadel"
      # Zitadel uses this claim for project roles (map format)
      groupsClaim: "urn:zitadel:iam:org:project:roles"
      # Optional: restrict access to specific roles
      allowedGroups: "developer,admin"
```

### 5. Upgrade

```bash
helm upgrade diverge diverge/diverge -f values.yaml
```

---

## GitHub (via Dex)

GitHub OAuth is **not** OIDC-compliant (it doesn't issue ID tokens). To use
GitHub for SSO, deploy Dex as an identity broker.

### 1. Create GitHub OAuth App

1. Go to GitHub → Settings → Developer settings → OAuth Apps → New
2. Application name: `Diverge Preview Platform`
3. Homepage URL: `https://diverge.example.com`
4. Authorization callback URL: `https://diverge.example.com/api/dex/callback`
5. Copy the **Client ID** and **Client Secret**

### 2. Deploy Dex

See the [Dex Getting Started Guide](https://dexidp.io/docs/getting-started/)
for deploying Dex alongside Diverge. Configure Dex with a GitHub connector:

```yaml
connectors:
  - type: github
    id: github
    name: GitHub
    config:
      clientID: "Iv1.your-client-id"
      clientSecret: "your-client-secret"
      orgs:
        - name: your-org
```

### 3. Point Diverge at Dex

```yaml
server:
  auth:
    oidc:
      enabled: true
      issuerUrl: "https://diverge.example.com/api/dex"
      clientId: "diverge"
      clientSecretSecretRef:
        name: "dex-client-secret"
        key: "clientSecret"
      redirectUrl: "https://diverge.example.com/auth/callback"
      providerName: "GitHub"
      groupsClaim: "groups"
```

---

## Restricting Access

### Allowed Groups

Restrict dashboard access to specific OIDC groups:

```yaml
server:
  auth:
    oidc:
      allowedGroups: "platform-team,sre-team"
```

Users not in any allowed group will see a `403 Forbidden` error.

### Session Duration

Control how long sessions last (default: 24 hours):

```yaml
server:
  auth:
    session:
      maxAge: 28800  # 8 hours
```

### Persistent Sessions

By default, session signing keys are generated on startup. Sessions are
invalidated when the server restarts. For persistent sessions, create a Secret:

```bash
# Generate a signing key
openssl rand -base64 32 | kubectl create secret generic diverge-session-secret \
  --namespace diverge \
  --from-file=session-secret=/dev/stdin
```

```yaml
server:
  auth:
    session:
      secretRef:
        name: "diverge-session-secret"
        key: "session-secret"
```

---

## Troubleshooting

### "OIDC discovery failed"

The server couldn't reach your OIDC provider's `/.well-known/openid-configuration`
endpoint. Check:
- The `issuerUrl` is correct and accessible from the cluster
- DNS resolution works in the pod network
- Any egress policies allow outbound HTTPS

### "redirect_uri_mismatch"

The callback URL configured in your OIDC provider doesn't match the
`redirectUrl` in Helm values. They must be exactly the same, including
trailing slashes and protocol.

### "user not in any allowed group"

The authenticated user's groups from the OIDC `groups` claim don't intersect
with `allowedGroups`. Verify the groups claim is being sent by checking the
ID token contents, or try clearing `allowedGroups` temporarily.

### SSO button shows "Not Configured"

The server is running without `--oidc-issuer-url`. Check that `server.auth.oidc.enabled`
is `true` in your Helm values and the deployment has been upgraded.
