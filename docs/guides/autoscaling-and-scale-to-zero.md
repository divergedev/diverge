# Autoscaling and Scale-to-Zero

Diverge integrates with [KEDA](https://keda.sh) to provide intelligent autoscaling for preview environment services, including the ability to scale idle services down to zero replicas.

## How It Works

| Protocol | KEDA Resource | Trigger | Scale Signal |
|----------|--------------|---------|--------------|
| HTTP | `HTTPScaledObject` | HTTP Add-on | Incoming requests |
| Temporal | `ScaledObject` | `temporal` | Task queue backlog |
| Kafka | `ScaledObject` | `kafka` | Consumer group lag |

## Configuration

Per-service KEDA settings in the `keda` block of each service spec:

```yaml
services:
  - name: api
    keda:
      minReplicas: 0       # Scale to zero when idle
      maxReplicas: 10
      cooldownPeriod: 300   # 5 min before scaling down

  - name: worker
    asyncRoutes:
      - protocol: temporal
        target: tasks
    keda:
      minReplicas: 0
      pollingInterval: 15    # Check queue every 15s
      targetQueueSize: 3     # Tasks per replica
```

### Field Reference

| Field | Default | Description |
|-------|---------|-------------|
| `minReplicas` | `1` | Minimum replicas. `0` enables scale-to-zero. |
| `maxReplicas` | `3` | Maximum replicas. |
| `cooldownPeriod` | `300` | Seconds before scaling down after last activity. |
| `pollingInterval` | `30` | Seconds between KEDA metric checks. |
| `targetQueueSize` | `5`/`10` | Target backlog per replica (Temporal/Kafka). |

### Config Precedence

1. Per-service CRD spec → 2. Controller CLI flags → 3. Built-in defaults

## Cold Start & Preview Wake-Up Splash Page

When scaling from zero, expect 10–60s latency (KEDA polling + pod scheduling + app startup). The HTTP Activator Proxy (`activator-proxy`) buffers the triggering request by default to prevent drops.

For interactive browser sessions (`GET` requests with `Accept: text/html`), `activator-proxy` also supports an auto-refreshing **Preview Wake-Up Splash Page**:
- **Enable via CLI flag**: Start `activator-proxy` with `--wake-splash`.
- **Enable per request**: Include the `X-Diverge-Wake-Splash: true` HTTP header.
- **How it works**: While the target pod is scaled to zero, `activator-proxy` triggers a background wake request and immediately responds with `202 Accepted` and an HTML splash screen that polls `GET /-/wakeup-status` (`{"ready": true|false}`) every 1.5 seconds, reloading automatically once the pod is ready.

## Annotation-Based Idle Hibernation

Environments can also hibernate without KEDA by setting idle hibernation annotations on the `Environment` CR:

- `divergedev.com/hibernate-after`: Duration string (e.g., `"15m"`, `"1h"`) after which an idle environment scales its Deployment to `0` replicas.
- `divergedev.com/last-traffic-at`: RFC 3339 timestamp recording the last observed traffic activity (falls back to `status.createdAt` if omitted).

