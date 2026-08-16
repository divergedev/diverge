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
   diverge dev --service my-api --env-output file -- air
   ```

Air reads `.env.diverge` and restarts `go build && ./tmp/main` on file changes.

## Node.js (with nodemon)

[nodemon](https://nodemon.io/) watches Node files and restarts on change.

```bash
diverge dev --service my-api --env-output file -- npx nodemon server.js
```

## Any Language (with watchexec)

[watchexec](https://watchexec.github.io/) is a language-agnostic file watcher.

```bash
diverge dev --service my-api --env-output file -- watchexec -r ./run.sh
```

## How It Works

1. `diverge dev` creates the Environment CR and waits for async routes to provision.
2. Once ready, it writes env vars to `.env.diverge` (with `--env-output file`).
3. The child process (Air/nodemon/watchexec) starts and watches for file changes.
4. On file change, the watcher restarts your app — env vars persist in `.env.diverge`.
5. On Ctrl+C, Diverge tears down the Environment and cleans up.

## `--env-output` Modes

| Mode | Behavior |
|------|----------|
| `inject` (default) | Env vars injected in-memory into the child process. Simple but requires restart of `diverge dev` to pick up new vars. |
| `file` | Env vars written to `.env.diverge`. File watchers like Air can source this file. Recommended for hot-reload workflows. |

## When to Use DevSpace Instead

If you need **in-cluster file sync** (e.g., syncing local files into a running pod), use the [DevSpace integration](devspace-integration.md) instead:

```bash
diverge dev --service my-api -- devspace dev
```

DevSpace handles bidirectional file sync, port forwarding, and remote debugging. Use it when your app needs cluster-local resources (databases, message brokers) that aren't available locally.
