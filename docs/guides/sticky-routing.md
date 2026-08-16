# Sticky Routing for Deep Links and SPAs

Diverge routes traffic to your preview environments using a custom HTTP header (e.g., `x-diverge-env`). However, this approach has limitations when:

1. A user clicks a deep link to your application (e.g., `https://preview.example.com/checkout`). The browser doesn't send the custom header.
2. Your frontend is a Single Page Application (SPA) that loses custom headers during client-side navigation.
3. Users bookmark a preview environment URL and return later.

To solve this, Diverge supports **sticky routing cookies**.

## How It Works

When sticky routing is enabled:

1. **Initial Request**: The user accesses the environment with the `x-diverge-env` header present. Diverge routes the request to the preview environment and attaches a `Set-Cookie` header to the response.
2. **Subsequent Requests**: For all subsequent requests (deep links, SPA navigation, returning via bookmark), the browser automatically includes the cookie. Diverge reads the cookie and correctly routes the request, even without the original header.
3. **Fallback**: If neither the header nor the cookie is present, the request falls back to the baseline environment.

## Enabling Sticky Routing

You can enable sticky routing by adding the `cookie` section to the `routing` block in your `Environment` specification:

```yaml
apiVersion: diverge.io/v1alpha1
kind: Environment
metadata:
  name: feat-login
spec:
  routing:
    mode: header
    headerKey: x-diverge-env
    headerValue: feat-login
    cookie:
      enabled: true
      maxAge: 86400        # Optional: Cookie max age in seconds (default: 86400, or 24 hours)
      sameSite: Lax        # Optional: SameSite policy (Lax, Strict, or None) (default: Lax)
```

### Configuration Options

* **`enabled`**: Set to `true` to enable cookie-based sticky routing.
* **`maxAge`**: The duration (in seconds) the cookie should be valid. Defaults to `86400` (24 hours). Adjust this based on how long you want preview environments to remain sticky in user sessions.
* **`sameSite`**: Controls the `SameSite` attribute of the cookie. Defaults to `Lax`.
  * `Lax`: The cookie is sent with safe top-level navigations (e.g., clicking a link).
  * `Strict`: The cookie is only sent in a first-party context.
  * `None`: The cookie is sent in all contexts, including cross-origin requests. (Requires a secure context).

## Supported Routers

Sticky routing via cookies is currently supported when using the **Gateway API** router (`routing.provider: gateway`). It automatically configures `HTTPRoute` rules with `ResponseHeaderModifier` to set the cookie and `RegularExpression` header matching to route requests based on the cookie value.

## Troubleshooting

* **Cookie Not Setting**: Ensure the initial request includes the `x-diverge-env` header. The cookie is only set on the response when the request successfully matches the header rule.
* **Cookie Not Sent**: If your SPA is making cross-origin requests to an API, ensure `sameSite` is configured appropriately (e.g., `None` if applicable) and your CORS configuration allows credentials.
* **Gateway API Support**: Sticky routing requires Gateway API implementations that support `RegularExpression` header matching and `ResponseHeaderModifier` filters.
