# Node.js Header Propagation

```javascript
app.use((req, res, next) => {
  const header = req.get('x-diverge-route');
  if (header) {
    // Propagate to outgoing requests
    req.divergeHeader = header;
  }
  next();
});
```
