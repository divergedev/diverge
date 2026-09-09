# Node.js Header Propagation

## Recommended: Diverge SDK

Install the SDK:

```bash
npm install @divergedev/sdk
```

### Express Middleware

```typescript
import express from 'express';
import { divergeMiddleware, getEnvironment } from '@divergedev/sdk';

const app = express();
app.use(divergeMiddleware());

app.get('/', (req, res) => {
  const env = getEnvironment(); // Automatically extracted from incoming request
  res.json({ environment: env });
});
```

### Outgoing HTTP Requests

The SDK wraps native `fetch()` to automatically inject the `x-diverge-env` header:

```typescript
import { divergeFetch } from '@divergedev/sdk';

// x-diverge-env header is automatically added from the current async context
const response = await divergeFetch('http://payments-api/charge');
```

### Full Binary Context (Advanced)

For richer metadata propagation (routing mode, custom metadata):

```typescript
import {
  PropagationContext,
  RoutingMode,
  encodePropagationContext,
  decodePropagationContext,
} from '@divergedev/sdk';

const ctx: PropagationContext = {
  environment: 'pr-42',
  routingMode: RoutingMode.HEADER,
  metadata: { team: 'payments' },
};
const encoded = encodePropagationContext(ctx);
// Wire-compatible with Go SDK via x-diverge-context-bin header
```

### AsyncLocalStorage Context

The SDK uses Node.js `AsyncLocalStorage` for context threading, which works across async boundaries:

```typescript
import { setEnvironment, getEnvironment } from '@divergedev/sdk';

// Context is automatically propagated through async boundaries
setEnvironment('pr-42', async () => {
  console.log(getEnvironment()); // "pr-42"

  // Even through async calls
  await someAsyncOperation();
  console.log(getEnvironment()); // Still "pr-42"
});
```

---

## Manual Approach

If you prefer not to use the SDK:

```javascript
app.use((req, res, next) => {
  const header = req.get('x-diverge-env');
  if (header) {
    // Propagate to outgoing requests
    req.divergeHeader = header;
  }
  next();
});
```
