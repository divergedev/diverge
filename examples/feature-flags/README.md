# Feature Flags Example

This example demonstrates how to configure feature flags for ephemeral preview environments using Diverge.

## Overview

Preview environments often require specific features to be toggled on or off during testing without polluting shared staging or production environments.

In this example:
- **Default preview environments** (`preview`) use the zero-dependency `configmap` provider, automatically creating a `flagd`-compatible JSON ConfigMap for each preview environment.
- **Staging previews** (`staging-preview`) demonstrate connecting to a remote [Flipt](https://flipt.io) engine, dynamically provisioning an isolated ephemeral namespace (`diverge-<envName>`) with boolean and variant flag overrides.
- **Enterprise previews** (`enterprise-preview`) demonstrate connecting to [Unleash](https://www.getunleash.io/), provisioning context-constrained `flexibleRollout` activation strategies (`diverge.environment IN [<envName>]`) and strategy-level variants.

---

## Directory Structure

```text
.
├── diverge.yaml          # Diverge preview configuration with feature flags
└── README.md             # This guide
```

---

## Prerequisites

1. A Kubernetes cluster with the **Diverge Operator** installed.
2. (Optional for Flipt) A running Flipt instance and a Kubernetes Secret containing connection credentials:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: flipt-credentials
  namespace: diverge-system
type: Opaque
stringData:
  url: "https://flipt.example.com"
  adminToken: "your-flipt-admin-token"
  clientToken: "your-flipt-client-token"
```

3. (Optional for Unleash) A running Unleash instance and a Kubernetes Secret containing `unleash-credentials`:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: unleash-credentials
  namespace: diverge-system
type: Opaque
stringData:
  url: "https://unleash.example.com"
  adminToken: "*:*.your-unleash-admin-token"
  clientToken: "default:development.your-unleash-client-token"
  project: "default"
  environment: "development"
```

---

## How It Works

### 1. In-Cluster ConfigMap Provider (`flagd`)

When `features.provider: configmap` is specified:
1. Diverge generates a ConfigMap named `<env-name>-features` containing an OpenFeature `flagd`-compliant flag definition.
2. Diverge injects:
   - `FLAGD_FLAG_PATH=/etc/diverge/flags.json`
   - `DIVERGE_FEATURE_CONFIGMAP=<env-name>-features`
3. Workload pods evaluate flags locally in-process without network overhead.

### 2. Remote Flipt Provider

When `features.provider: flipt` is specified:
1. Diverge contacts the Flipt API at the secret's `url`.
2. Diverge creates an isolated namespace `diverge-<envName>` and writes the configured flag overrides.
3. Workloads receive `FLIPT_URL`, `FLIPT_NAMESPACE`, and `FLIPT_AUTH_TOKEN`.
4. When the preview environment expires or is destroyed, Diverge idempotently deletes the Flipt namespace.

### 3. Remote Unleash Provider

When `features.provider: unleash` is specified:
1. Diverge contacts the Unleash Admin API at the secret's `url` using `adminToken`.
2. Diverge ensures each overridden feature toggle exists and attaches a preview-scoped `flexibleRollout` strategy (`diverge-[<namespace>-]<envName>-<hash8>`) constrained to `diverge.environment IN [<envName>]`.
3. Workloads receive `UNLEASH_URL`, `UNLEASH_API_TOKEN` (from `clientToken`), `UNLEASH_PROJECT`, `UNLEASH_ENVIRONMENT`, `UNLEASH_STRATEGY_TITLE`, and `OPENFEATURE_TARGET_KEY`.
4. When the preview environment expires or is destroyed, Diverge removes the preview activation strategies and exclusion constraints cleanly.

---

## Consuming Flags in Code

Workloads use the standard [OpenFeature](https://openfeature.dev/) SDK:

### Go Example

```go
import (
    "context"
    "os"
    "github.com/open-feature/go-sdk/openfeature"
    flagd "github.com/open-feature/go-sdk-contrib/providers/flagd/pkg/service/in-process"
)

func main() {
    flagPath := os.Getenv("FLAGD_FLAG_PATH")
    if flagPath != "" {
        openfeature.SetProvider(flagd.NewProvider(flagd.WithFlagPath(flagPath)))
    }
    client := openfeature.NewClient("app")
    enabled, _ := client.BooleanValue(context.Background(), "new_nav_bar", false, openfeature.EvaluationContext{})
}
```

For more language SDK examples (Node.js, Python), see the [Feature Flags Guide](../../docs/guides/feature-flags.md).
