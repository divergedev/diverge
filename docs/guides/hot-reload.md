# Hot Reload with diverge dev

`diverge dev` manages the Diverge lifecycle (Environment creation, async route provisioning, env var injection) and spawns your app as a child process. To add file-watching and auto-restart, pair it with a file watcher.

## Go (with Air)

[Air](https://github.com/air-verse/air) watches Go files and rebuilds on change.

1. Install Air:
   ```bash
   go install github.com/air-verse/air@latest
   ```

2. Run with Diverge:
   ```bash
   # In-memory injection (recommended):
   diverge dev --service my-api -- air
   ```

In `--env-output inject` (the default mode), `diverge dev` injects environment variables directly into the Air process environment, which propagates them to your compiled application binary upon restart.

> **Note on `--env-output file`**: If you use `--env-output file`, Diverge writes variables to `.env.diverge`. Air does not read `.env.diverge` by default — you must configure Air (via `.air.toml` / build commands) or use an env loader (such as `godotenv`) in your application to source the file.

## Node.js (with nodemon)

[nodemon](https://nodemon.io/) watches Node files and restarts on change.

```bash
diverge dev --service my-api -- npx nodemon server.js
```

## Any Language (with watchexec)

[watchexec](https://watchexec.github.io/) is a language-agnostic file watcher.

```bash
diverge dev --service my-api -- watchexec -r ./run.sh
```

## How It Works

1. `diverge dev` creates the Environment CR and waits for async routes to provision.
2. Once ready, it injects baseline env vars into the child process environment (or writes to `.env.diverge` with `--env-output file`).
3. The child process (Air/nodemon/watchexec) starts and watches for file changes.
4. On file change, the watcher restarts your app with the environment variables preserved.
5. On Ctrl+C, Diverge tears down the Environment and cleans up.

## `--env-output` Modes

| Mode | Behavior |
|------|----------|
| `inject` (default) | Env vars injected in-memory into the child process (Air/nodemon/watchexec) and inherited by restarted binaries. Recommended for most hot-reload workflows. |
| `file` | Env vars written to `.env.diverge`. Requires configuring your file watcher or application to source the `.env.diverge` file. |

## When to Use DevSpace Instead

If you need **in-cluster file sync** (e.g., syncing local files into a running pod), use the [DevSpace integration](devspace-integration.md) instead:

```bash
diverge dev --service my-api -- devspace dev
```

DevSpace handles bidirectional file sync, port forwarding, and remote debugging. Use it when your app needs cluster-local resources (databases, message brokers) that aren't available locally.
