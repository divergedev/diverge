# Diverge CLI Reference

The `diverge` command-line interface is the primary tool for developers to interact with Diverge environments. It provides commands for creating, listing, deleting, and managing preview environments and local dev sessions.

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

## `diverge preview create`

Creates a new preview environment.

```bash
diverge preview create
```

## `diverge preview delete`

Deletes a preview environment.

```bash
diverge preview delete <name>
```

## `diverge preview list`

Lists active preview environments.

```bash
diverge preview list
```

## `diverge preview status`

Shows the detailed status of a preview group or environment.

```bash
diverge preview status <name>
```

## `diverge preview intercept`

Intercepts a service within an existing preview group, routing its traffic to a local endpoint instead of running a cluster image.

```bash
diverge preview intercept <service> --group <group-name> --endpoint <local-ip:port>
```

## `diverge preview release`

Releases an intercepted service, reverting it to its normal image mode.

```bash
diverge preview release <service> --group <group-name>
```

## `diverge providers list`

Lists all registered Diverge providers (routers, deployers, databases, async-provisioners, etc.) currently built into the controller.

```bash
diverge providers list --output table
```

**Flags:**
- `--output` / `-o`: Output format (`table`, `json`, `yaml`).

## `diverge env export`

Fetches environment variables from the baseline environment's pod so you can connect your local development setup to baseline dependencies.

```bash
diverge env export --service frontend --format dotenv > .env.preview
```

## `diverge status`

Shows detailed status of a specific environment resource, including async routes and conditions.

```bash
diverge status <environment-name>
```
