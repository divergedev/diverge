import { getHeaderKey, setEnvironment } from './context.js';

export function extractEnvironment(headers: Record<string, string | string[] | undefined>): string | undefined {
  const key = getHeaderKey().toLowerCase();

  // Find key case-insensitively
  for (const [k, v] of Object.entries(headers)) {
    if (k.toLowerCase() === key && v) {
      if (Array.isArray(v)) {
        return v[0];
      }
      return v;
    }
  }

  return undefined;
}

export function divergeMiddleware() {
  return function(req: any, res: any, next: (err?: any) => void) {
    const env = extractEnvironment(req.headers || {});

    if (env) {
      setEnvironment(env, () => next());
    } else {
      next();
    }
  };
}
