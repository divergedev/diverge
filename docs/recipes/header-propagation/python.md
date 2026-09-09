# Python Header Propagation

## Recommended: Diverge SDK

Install the SDK:

```bash
uv pip install diverge-sdk
# Or with web framework support:
uv pip install "diverge-sdk[web]"
```

### FastAPI / Starlette (ASGI)

```python
from diverge_sdk import DivergASGIMiddleware, get_environment
from fastapi import FastAPI

app = FastAPI()
app.add_middleware(DivergASGIMiddleware)

@app.get("/")
async def handler():
    env = get_environment()  # Automatically extracted from incoming request
    return {"environment": env}
```

### Outgoing HTTP Requests

The SDK automatically injects the `x-diverge-env` header on outgoing requests:

```python
from diverge_sdk.requests_transport import DivergeSession

session = DivergeSession()
# x-diverge-env header is automatically added from the current context
response = session.get("http://payments-api/charge")
```

### Full Binary Context (Advanced)

For richer metadata propagation (routing mode, custom metadata):

```python
from diverge_sdk import PropagationContext, RoutingMode
from diverge_sdk import encode_propagation_context, decode_propagation_context

ctx = PropagationContext(
    environment="pr-42",
    routing_mode=RoutingMode.HEADER,
    metadata={"team": "payments"},
)
encoded = encode_propagation_context(ctx)
# Wire-compatible with Go SDK via x-diverge-context-bin header
```

---

## Manual Approach

If you prefer not to use the SDK:

```python
@app.middleware("http")
async def propagate_diverge_header(request: Request, call_next):
    header = request.headers.get("x-diverge-env")
    if header:
        diverge_context.set(header)
    response = await call_next(request)
    return response
```
