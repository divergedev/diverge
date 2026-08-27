# E2E Tests

End-to-end tests run against a real Kind cluster to verify the controller's reconciliation logic.

## Prerequisites

- Docker (for Kind cluster)
- `kind` CLI
- `kubectl`
- Go 1.26+

All tools are available in the nix dev shell: `nix develop`

## Running

```bash
# Full lifecycle: create cluster, build images, deploy, run tests, teardown
make e2e

# Individual steps:
make e2e-setup    # Create Kind cluster + deploy controller
make e2e-run      # Run tests
make e2e-teardown # Delete Kind cluster
```

## Writing E2E Tests

### Build Tag

All E2E test files **must** include the build tag:

```go
//go:build e2e

package e2e
```

### Framework

Use the `Framework` helper from `framework.go`:

```go
func TestMyFeature(t *testing.T) {
    f := NewFramework(t)
    ctx := context.Background()
    f.CreateNamespace(ctx)
    defer f.CleanupNamespace(ctx)

    // Create an Environment CR
    env := &v1alpha1.Environment{
        ObjectMeta: metav1.ObjectMeta{
            Name:      "my-test",
            Namespace: f.Namespace,
        },
        Spec: v1alpha1.EnvironmentSpec{
            Source: v1alpha1.EnvironmentSource{
                Provider: "github",
                Project:  "org/repo",
                Branch:   "feat/test",
            },
        },
    }
    err := f.CreateEnvironment(ctx, env)
    require.NoError(t, err)

    // Skip reconciliation checks if controller isn't deployed
    if !f.ControllerRunning(ctx) {
        t.Skip("controller not deployed")
    }

    // Wait for a condition
    err = f.WaitForCondition(ctx, env.Name, "Ready", metav1.ConditionTrue, 2*time.Minute)
    require.NoError(t, err)
}
```

### Framework Methods

| Method | Description |
|--------|-------------|
| `NewFramework(t)` | Creates framework with unique namespace |
| `CreateNamespace(ctx)` | Creates the test namespace |
| `CleanupNamespace(ctx)` | Deletes the test namespace |
| `CreateEnvironment(ctx, env)` | Creates an Environment CR |
| `WaitForCondition(ctx, name, type, status, timeout)` | Polls until condition matches |
| `WaitForEnvironmentDeleted(ctx, name, timeout)` | Polls until Environment is gone |
| `ControllerRunning(ctx)` | Checks if controller deployment is ready |

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `E2E_TIMEOUT` | `3m` | Default wait timeout |
| `DIVERGE_CONTROLLER_NAMESPACE` | `diverge-system` | Controller namespace |

### Conventions

- Use `alpine:3.20` for hook test images (PSS compliant, fast pull)
- Use `require.Eventually` for polling assertions
- Always `defer f.CleanupNamespace(ctx)` to avoid namespace leaks
- Use descriptive env names: `hook-success`, `hook-fail`, `hook-cleanup`
