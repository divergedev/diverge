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

## Cold Start

When scaling from zero, expect 10–60s latency (KEDA polling + pod scheduling + app startup). The HTTP Activator Proxy buffers the triggering request to prevent drops.
