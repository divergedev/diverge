# Preview Environment Banner

When a developer accesses a preview environment URL, they might forget they are not looking at production. To prevent confusion, Diverge can inject a visual indicator banner into preview environments.

## How It Works

Diverge can create a `ConfigMap` containing a small JavaScript snippet that renders a fixed-position floating banner on the page.

## Enabling the Banner

To enable the banner, add the `banner` configuration to the `routing` section of your `Environment` specification:

```yaml
apiVersion: diverge.io/v1alpha1
kind: Environment
metadata:
  name: my-preview
spec:
  # ...
  routing:
    banner:
      enabled: true
      text: "Preview Environment"
      position: top
      color: "#FF6B00"
```

### Configuration Options

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | boolean | `false` | Whether to generate the banner ConfigMap |
| `text` | string | `"Preview Environment"` | Text to display in the banner |
| `position` | string | `"top"` | Position of the banner (`top` or `bottom`) |
| `color` | string | `"#FF6B00"` | Background color of the banner (CSS color) |

## Using the Banner in Your App

When the banner is enabled, Diverge creates a `ConfigMap` named `diverge-preview-banner` in the environment's namespace. It contains a single key `diverge-banner.js` with the banner script.

You can inject this into your application in several ways:

### Option A: Mount as a Volume

Mount the `ConfigMap` as a volume in your application Pod and serve it as a static asset, or include it in your HTML response.

```yaml
volumes:
  - name: preview-banner
    configMap:
      name: diverge-preview-banner
      optional: true

containers:
  - name: my-app
    volumeMounts:
      - name: preview-banner
        mountPath: /var/www/html/banner
```

Then in your HTML:
```html
<script src="/banner/diverge-banner.js"></script>
```

### Option B: Inject via Proxy/Middleware

If you have a proxy or API gateway in front of your application, you can configure it to append the script to HTML responses.
