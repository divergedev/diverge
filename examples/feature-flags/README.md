# Feature Flags Example

This example demonstrates how to configure feature flags for ephemeral preview environments using Diverge.

## Overview

Preview environments often require specific features to be toggled on or off during testing without polluting shared staging or production environments.

In this example:
- **Default preview environments** (`preview`) use the zero-dependency `configmap` provider, automatically creating a `flagd`-compatible JSON ConfigMap for each preview environment.
- **Staging previews** (`staging-preview`) demonstrate connecting to a remote [Flipt](https://flipt.io) engine, dynamically provisioning an isolated ephemeral namespace (`diverge-<envName>`) with boolean and variant flag overrides.

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
