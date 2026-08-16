# Python Header Propagation

```python
@app.middleware("http")
async def propagate_diverge_header(request: Request, call_next):
    header = request.headers.get("x-diverge-route")
    if header:
        diverge_context.set(header)
    response = await call_next(request)
    return response
```
