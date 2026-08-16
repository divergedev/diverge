# CLI Reference

The `diverge` CLI tool is the primary way developers interact with preview environments from their local machines. It allows you to create environments, stream logs, sync local changes, and manage your preview contexts.

## `diverge dev`

Starts a local development loop connected to a remote preview environment.

**Key behaviors:**
- **Async Blocking**: The CLI will block and wait until all required asynchronous resources (like Temporal task queues and Kafka topics) are fully provisioned and `Ready` before proceeding.
- **Environment Variable Sync**: It fetches the remote environment configuration and syncs the injected environment variables (e.g., `TEMPORAL_TASK_QUEUE`, `DIVERGE_ENV_ID`) locally to your development process.
- **Process Lifecycle**: It manages the lifecycle of your local service, restarting it if remote dependencies change or if requested.

**Usage:**
```bash
diverge dev --service-name my-app
```

## `diverge status`

Shows the active preview environments and their current phases (e.g., `Deploying`, `Running`, `Failed`).

**Usage:**
```bash
diverge status
```

**Output:**
```
ID        BRANCH               PHASE     AGE   URL
mr-123    feature/new-login    Running   2h    https://mr-123.preview.mycorp.com
mr-124    fix/typo             Deploying 1m    -
```

## `diverge logs`

Streams live logs directly from the pods running in your preview environment.

**Usage:**
```bash
# Stream logs for all services in the mr-123 environment
diverge logs --env mr-123

# Stream logs for a specific service
diverge logs --env mr-123 --service payments
```

## `diverge env export --group`

Exports the environment variable configurations for a specific PreviewGroup to be used in shell scripts or local IDEs. This is incredibly useful for workflow automation.

**Usage / Workflow Examples:**

```bash
# Export variables and evaluate them in the current shell
eval $(diverge env export --group mr-123 --format export)

# Output as JSON for integration with other tools
diverge env export --group mr-123 --format json > .env.preview
```

## `diverge providers list`

Lists all currently registered providers (databases, notifiers, routing engines) compiled into the controller.

**Usage:**
```bash
diverge providers list --output table
```

**Output Formats:**
Supported `--output` formats include `table`, `json`, and `yaml`.

```bash
# Example JSON output
diverge providers list --output json
```
