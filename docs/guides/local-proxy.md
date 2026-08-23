# Local Loopback Proxy

## Overview
The proxy sits locally during `diverge dev` and routes outbound service calls through preview environments, automatically injecting the routing header.

## Path mode quickstart (default)
```bash
diverge dev -- go run ./cmd/myapp
# Your app calls: http://127.0.0.1:19001/cart-service/api/items
```

## Host mode quickstart
```bash
diverge dev --proxy-mode host -- go run ./cmd/myapp
# Your app calls: http://cart-service.localhost:19001/api/items
```

## Which mode should I use?

| | Path (default) | Host |
|---|---|---|
| URL format | `http://localhost:19001/svc-name/path` | `http://svc-name.localhost:19001/path` |
| DNS setup | None | Requires `*.localhost` (macOS/Linux) |
| Docker | Works with mapped ports | Needs `--network=host` (Linux only) |
| Best for | Default, always works | Cleaner URLs, no path prefix |

## ConnectRPC streaming
ConnectRPC clients get full streaming support (unary, client streaming, server streaming) because the Connect wire protocol uses standard HTTP. Bidirectional streaming requires HTTP/2 (h2c), which the proxy supports automatically.

Example:
```go
client := examplev1connect.NewExampleServiceClient(
    http.DefaultClient,
    os.Getenv("DIVERGE_PROXY_URL") + "/cart-service",
)
```
*Note: when using the proxy locally, you don't need `divergeconnect.PropagateEnvironment()` — the proxy injects the routing header for you.*

## Raw gRPC
Note that `application/grpc` wire protocol is not supported. Use Connect protocol instead.

## Environment variables
- `DIVERGE_PROXY_URL` — concrete base URL (e.g., `http://127.0.0.1:19001`)
- `DIVERGE_PROXY_MODE` — `path` or `host`
- `DIVERGE_SVC_{NAME}_URL` — direct service URLs (for in-cluster use)

The proxy is recommended for local dev (auto header injection), while `DIVERGE_SVC_*_URL` are used for direct/in-cluster access.

## CLI flags
- `--proxy-port`: Sets the port for the local proxy.
- `--no-proxy`: Disables the local proxy.
- `--proxy-mode`: Sets the proxy mode (`path` or `host`).

## DNS auto-fallback
If `*.localhost` doesn't resolve in host mode, diverge automatically falls back to path mode with a warning.
