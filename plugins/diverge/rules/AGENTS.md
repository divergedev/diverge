# Diverge Workspace & Preview Rules

When working in a repository configured with Diverge (`.diverge.yaml`, `diverge.yaml`, or Kubernetes `divergedev.com/v1alpha1` custom resources):

1. **Validate `.diverge.yaml` After Edits**:
   - Whenever creating or modifying `.diverge.yaml`, ensure the YAML language-server schema header (`# yaml-language-server: $schema=https://divergedev.com/schemas/diverge.schema.json`) is present at the top of the file and run `diverge validate -c .diverge.yaml`.
2. **Use `divergedev.com/v1alpha1` API Group & Labels**:
   - Always use `apiVersion: divergedev.com/v1alpha1` (never the legacy `diverge.io/v1alpha1` group) for `Environment`, `PreviewGroup`, and `AgentTask` manifests, and use `divergedev.com/*` labels and annotations.
3. **Prefer Delta Deploys & Header/Cookie Routing**:
   - Default to `deploy.mode: delta` and `routing.headerKey: x-diverge-env` so only changed microservices (`diverge diff --base main`) are deployed per Pull Request.
   - Use `?_diverge_env=<env>` (`diverge open --magic <env>`) for browser sticky-cookie routing through `diverge-proxy`, and `diverge curl <env> <path> --compare-baseline` to verify preview responses against baseline.
4. **Use Structured Diagnostics Before Guessing**:
   - When a preview `Environment`, database migration job, or `AgentTask` sandbox is unhealthy, run `diverge doctor <name> --json` (or the `diverge_doctor` MCP tool) to inspect `DIV-101`–`DIV-110` diagnostic codes and remediation commands.
