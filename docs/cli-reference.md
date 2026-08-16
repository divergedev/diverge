# Diverge CLI Reference

The `diverge` command-line interface is the primary tool for developers to interact with Diverge environments.

## `diverge create`

Creates a new preview environment group.

```bash
diverge create
```

## `diverge delete`

Deletes a preview environment group.

```bash
diverge delete <name>
```

## `diverge dev`

Starts a local development session by creating a `PreviewGroup` that routes traffic for the specified service to your local machine. This command implements async blocking and supports environment variable injection from the baseline service.

```bash
diverge dev --service backend --port 8080 --endpoint 100.1.2.3:8080
```

**Flags:**
- `--service`: Service name (auto-detected if omitted).
- `--port`: Local port (default: `8080`).
- `--endpoint`: Local endpoint IP (auto-detected to tailscale ip).
- `--env-output`: How to handle env vars: `inject` (in-memory execution), `file` (writes to `.env.diverge`).

## `diverge env export`

Fetches environment variables from the baseline environment's pod so you can connect your local development setup to baseline dependencies.

```bash
diverge env export --service frontend --format dotenv > .env.preview
```

**Output Formats:**
Supported formats for the `--format` flag include `dotenv`, `json`, and `shell`.

## `diverge init`

Initializes Diverge configuration in a repository.

```bash
diverge init
```

## `diverge list`

Lists active preview environments.

```bash
diverge list
```

## `diverge logs`

Views logs for a preview environment service.

```bash
diverge logs <name>
```

## `diverge open`

Opens the URL for a preview environment.

```bash
diverge open <name>
```

## `diverge plugins`

Manage installed plugins.

```bash
diverge plugins list
```

## `diverge preview`

Manage preview environments (has subcommands like `create`, `status`, `delete`, `watch`, `intercept`, `release`).

## `diverge providers list`

Lists all registered Diverge providers (routers, deployers, databases, async-provisioners, etc.) currently built into the controller.

```bash
diverge providers list --output table
```

**Flags:**
- `--output` / `-o`: Output format (`table`, `json`, `yaml`).

## `diverge status`

Shows detailed status of a specific environment resource, including async routes and conditions.

```bash
diverge status <environment-name>
```

## `diverge validate`

Validates `.diverge.yaml` configuration.

```bash
diverge validate
```

## `diverge version`

Prints the Diverge CLI version.

```bash
diverge version
```
