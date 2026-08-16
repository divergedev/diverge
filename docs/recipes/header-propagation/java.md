# Java Header Propagation

```java
@Component
public class DivergeHeaderFilter implements Filter {
    @Override
    public void doFilter(ServletRequest req, ServletResponse res, FilterChain chain) {
        String header = ((HttpServletRequest) req).getHeader("x-diverge-route");
        if (header != null) {
            DivergeContext.set(header);
        }
        chain.doFilter(req, res);
        DivergeContext.clear();
    }
}
```
