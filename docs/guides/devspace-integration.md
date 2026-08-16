# DevSpace Integration with Diverge

This guide explains how to use [DevSpace](https://devspace.sh) alongside Diverge for a richer local development loop with file sync, hot reloading, and bi-directional traffic routing.

## Prerequisites
- **Diverge CLI** installed and configured
- **DevSpace CLI** installed (`npm install -g devspace` or equivalent)
- **Tailscale** installed and running

## How it works
Normally, `diverge dev --service <name>` routes cluster traffic to your local machine, where you run your app manually.
With DevSpace, you can run your app *inside* a local container (or DevSpace pipeline), utilizing its powerful file syncing and port forwarding.

The integration uses a nested command pattern:
```bash
diverge dev --service my-service -- devspace dev
```
1. Diverge creates the PreviewGroup and routes traffic to your machine.
2. Diverge starts `devspace dev` as a child process.
3. DevSpace handles building, syncing files, and starting your local server.

## Getting Started

1. Create a `devspace.yaml` in your project root using our reference template:
   ```bash
   diverge dev --devspace
   ```
   (Or copy the `devspace.yaml` from the root of the Diverge repository).

2. Modify `devspace.yaml` to match your service:
   - Ensure the `imageSelector` matches your cluster image name.
   - Adjust `ports` and `sync` as needed.
   - Update the `terminal.command` to point to your start script.

3. Run the pipeline:
   ```bash
   devspace run diverge-dev
   ```
   Or set the variable and run:
   ```bash
   DIVERGE_SERVICE=my-service devspace run diverge-dev
   ```

## Remote Development (In-Cluster)

For services with heavy cluster dependencies (databases, Temporal, Kafka), you can develop directly inside the cluster:

1. Diverge creates the preview environment and routes traffic
2. DevSpace syncs your local files TO a dev container in the preview namespace
3. Your code runs IN the cluster with full access to cluster services

### Remote Dev Template

```yaml
version: v2beta1
name: diverge-remote-dev

pipelines:
  diverge-remote:
    run: |-
      diverge dev --service ${DIVERGE_SERVICE} -- devspace dev --no-warn

dev:
  app:
    imageSelector: ${DIVERGE_SERVICE}
    devImage: golang:1.26  # or your preferred dev image
    sync:
      - path: ./:/app
        excludePaths:
          - .git/
          - vendor/
          - node_modules/
    terminal:
      command: /bin/sh
    env:
      - name: DIVERGE_ENV
        value: ${DIVERGE_ENV}
    ports:
      - port: "8080:8080"
```

Run with: `devspace run diverge-remote`

### When to use Remote vs Local

| | Local Dev | Remote Dev |
|--|-----------|------------|
| Speed | Faster iteration | Network latency |
| Dependencies | Needs Tailscale | Direct cluster access |
| Best for | Frontend, simple services | Backend with DB/queues |
| Env vars | Injected by diverge dev | Inherited from namespace |

## Comparison: Standalone vs DevSpace

| Feature | `diverge dev` Standalone | `diverge dev` + DevSpace |
|---------|--------------------------|--------------------------|
| **Cluster Traffic** | Routes to localhost | Routes to localhost |
| **Env Vars** | Synced natively | Synced to child process |
| **File Sync** | Manual/Local only | Automatic sync to container |
| **Hot Reload** | Manual/Local only | Container-based hot reload |
| **Dependencies**| Run locally (e.g. Node, Go) | Run in DevSpace container |

## Example
See the `examples/devspace/` directory for a minimal complete setup.
