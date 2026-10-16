# Diverge Node.js SDK (`@divergedev/sdk`)

Cross-language context propagation and sandbox metering SDK for the Diverge platform.

## Installation

```bash
npm install @divergedev/sdk
```

## Features

- **HTTP & Express Context Propagation**: `divergeMiddleware`, `divergeFetch`, `setEnvironment`, `getEnvironment`.
- **LLM Usage Metering**: `meterLLMUsage`, `LLMMeterError`, `resolveSandboxToken` for reporting model token and cost usage from agent sandboxes to the Diverge control plane.
- **Kafka Propagation & Topic Resolution**: `injectKafkaHeaders`, `withKafkaMessage`, `resolveKafkaTopic`.
- **Temporal Workflow & Activity Propagation**: `getTemporalHeaderMap`, `injectTemporalHeaders`, `withTemporalHeaders`.
- **Redis / Valkey Key Prefix Isolation**: `resolveRedisPrefix`, `resolveRedisKey`.

## Usage

### LLM Usage Metering

```ts
import { meterLLMUsage, LLMMeterError } from '@divergedev/sdk';

try {
  const result = await meterLLMUsage({
    model: 'claude-sonnet-4-6',
    tokensUsed: 1200,
    costUSD: 0.018,
  });
  console.log('Total tokens used:', result.totalTokensUsed);
} catch (err) {
  if (err instanceof LLMMeterError) {
    if (err.isBudgetExceeded) {
      console.error('Sandbox budget exceeded:', err.message);
    } else if (err.isModelDisallowed) {
      console.error('Model not allowed by policy:', err.message);
    }
  }
}
```

### Kafka Propagation

```ts
import { injectKafkaHeaders, withKafkaMessage, resolveKafkaTopic } from '@divergedev/sdk';

// Producer: resolve preview topic and inject routing headers
const topic = resolveKafkaTopic('orders.events');
const headers = injectKafkaHeaders({ 'x-correlation-id': '123' });

// Consumer: restore preview environment context from incoming message
withKafkaMessage(message, () => {
  // Inside this callback, getEnvironment() returns the propagated preview environment
});
```

### Temporal Propagation

```ts
import { getTemporalHeaderMap, injectTemporalHeaders, withTemporalHeaders } from '@divergedev/sdk';

// Inject active preview context into Temporal headers
const headers = injectTemporalHeaders();

// Restore preview context inside an activity or workflow interceptor
withTemporalHeaders(headers, () => {
  // Active preview environment restored
});
```

### Redis / Valkey Key Prefix Isolation

```ts
import { resolveRedisPrefix, resolveRedisKey } from '@divergedev/sdk';

const key = resolveRedisKey('session:user-123');
// Returns 'preview:pr-42:session:user-123' in preview environments, or 'session:user-123' in baseline
```

