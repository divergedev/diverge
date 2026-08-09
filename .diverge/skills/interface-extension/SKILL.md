# Diverge Interface Extension Design Patterns

## Purpose
How to extend Diverge's pluggable interfaces when adding new integrations (deployers, SCM providers, routers, database providers).

## Design Principles

1. **Thin interfaces, grow from pull** — Start with the minimum methods needed. Add methods when a real user/implementation requires them, not speculatively. Example: `Deployer` started as 2 methods (Deploy + Teardown). `Status()` will be added when someone needs deployment health reporting.

2. **Don't rename working packages** — If a package works and has tests, don't rename it for aesthetics. The `notifier/` package stays as `notifier/` even though it could be called `scm/`. Renames break git blame and import paths for zero functional change.

3. **Wrap, don't move** — When creating a new abstraction over existing code (e.g., `deployer.ArgoDeployer` wrapping `argocd.Client`), create a wrapper in the new package that delegates to the existing one. Don't move the existing code.

4. **Interface compliance at compile time** — Always add `var _ Interface = (*Implementation)(nil)` to verify implementations satisfy interfaces.

5. **Noop implementations are first-class** — Every interface should have a `Noop` implementation. It's used for testing, for optional features, and as the default when no provider is configured.

6. **Optional in the reconciler** — Interface fields in `EnvironmentReconciler` should be nil-checked before use. This lets operators run with only the subsystems they need.

7. **Flag-based wiring** — Provider selection happens via CLI flags in `cmd/controller/main.go`, not via config files or environment variables. The flag selects which implementation to instantiate.

## Current Interfaces

| Interface | Package | Implementations | Purpose |
|-----------|---------|----------------|----------|
| `Deployer` | `internal/deployer` | `ArgoDeployer`, `NoopDeployer` | Deploy/teardown services |
| `Notifier` | `internal/notifier` | `GitLabNotifier`, `GitHubNotifier`, `NoopNotifier` | MR/PR comment notifications |
| `Router` | `internal/routing` | `GRPCRouter`, `GatewayRouter`, `IstioRouter` | K8s networking for traffic routing |
| `DatabaseProvider` | `internal/database` | `SharedProvider`, `SchemaProvider` | Database provisioning per environment |
| `ChangeDetector` | `internal/changeset` | `GitChangeDetector` | Detect which services changed |

## How to Add a New Implementation

1. Create `internal/<package>/<name>.go` implementing the interface
2. Add `var _ Interface = (*Name)(nil)` compile check
3. Add constructor `NewName(...) *Name`
4. Write tests in `internal/<package>/<name>_test.go`
5. Add a case to the switch in `cmd/controller/main.go`
6. Add the CLI flag value to the flag description
7. Document in this skill

## How to Extend an Interface

When to add a method:
- A real implementation needs it (not speculative)
- The method makes sense across ALL implementations (including Noop)
- The Noop implementation can return a sensible zero value

Process:
1. Add the method to the interface
2. Add it to ALL implementations (including Noop)
3. Update the reconciler to call it (with nil-check)
4. Update tests
5. Document the method's purpose

## Future Extension Points

Known future interfaces that will be extracted when needed:
- `CommitReporter` — Set commit status on SCM (will be added to `scm/` package when a user needs merge gating)
- `DeploymentTracker` — Register deployments in SCM platform (GitLab Deployment Environments API, GitHub Deployments API)
- `Deployer.Status()` — Deployment health reporting (will be added when MR comments need per-service sync status)

These are NOT designed yet. They will be designed when the first user asks for them.

## Anti-patterns

1. ❌ Don't create interfaces before you have the second implementation
2. ❌ Don't rename packages for consistency
3. ❌ Don't add speculative methods
4. ❌ Don't create fat interfaces (>4 methods is a smell)
5. ❌ Don't make interface fields required in the reconciler
