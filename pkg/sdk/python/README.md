# Diverge Python SDK

Cross-language context propagation and sandbox metering SDK for the diverge platform.

## Installation

```bash
pip install diverge-sdk
# With requests support (for DivergeSession):
pip install diverge-sdk[requests]
```

## Features
- Context propagation for ASGI/WSGI applications.
- Automatic injection of contexts into outgoing requests.
- **LLM Usage Metering**: `meter_llm_usage`, `LLMMeterError`, `LLMMeterResponse`, `resolve_sandbox_token` (zero extra dependencies).
- **Kafka Propagation & Topic Resolution**: `inject_kafka_headers`, `extract_kafka_context`, `kafka_message_context`, `resolve_kafka_topic`.
- **Temporal Propagation**: `get_temporal_headers`, `temporal_header_context`.
- **Redis / Valkey Key Prefix Isolation**: `resolve_redis_prefix`, `resolve_redis_key`.

## Usage

### LLM Usage Metering

```python
from diverge_sdk import meter_llm_usage, LLMMeterError

try:
    resp = meter_llm_usage("claude-sonnet-4-6", tokens_used=1200, cost_usd=0.018)
    print("Total tokens used:", resp.total_tokens_used)
except LLMMeterError as err:
    if err.is_budget_exceeded:
        print("Sandbox budget exceeded:", err)
    elif err.is_model_disallowed:
        print("Model disallowed by policy:", err)
```

### Kafka Propagation

```python
from diverge_sdk import inject_kafka_headers, kafka_message_context, resolve_kafka_topic

# Producer
topic = resolve_kafka_topic("orders.events")
headers = inject_kafka_headers([("x-correlation-id", b"123")])

# Consumer
with kafka_message_context(msg.headers()):
    # Preview environment context is active in this block
    pass
```

### Temporal Propagation

```python
from diverge_sdk import get_temporal_headers, temporal_header_context

# Outbound workflow/activity headers
headers = get_temporal_headers()

# Inbound activity/workflow context restoration
with temporal_header_context(incoming_headers):
    pass
```

### Redis / Valkey Key Prefix Isolation

```python
from diverge_sdk import resolve_redis_prefix, resolve_redis_key

key = resolve_redis_key("session:user-123")
# Returns "preview:pr-42:session:user-123" in preview environments, or "session:user-123" in baseline
```


