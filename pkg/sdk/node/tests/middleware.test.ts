import { describe, it, expect } from 'vitest';
import { divergeMiddleware } from '../src/middleware.js';
import { getEnvironment } from '../src/context.js';

describe('Middleware', () => {
  it('should extract header and set environment', () => {
    const req = {
      headers: {
        'x-diverge-env': 'middleware-env'
      }
    };
    const res = {};

    const middleware = divergeMiddleware();

    let executed = false;

    middleware(req, res, () => {
      expect(getEnvironment()).toBe('middleware-env');
      executed = true;
    });

    expect(executed).toBe(true);
    // outside the middleware context, should be empty
    expect(getEnvironment()).toBe('');
  });

  it('should handle no header', () => {
    const req = {
      headers: {}
    };
    const res = {};

    const middleware = divergeMiddleware();

    let executed = false;

    middleware(req, res, () => {
      expect(getEnvironment()).toBe('');
      executed = true;
    });

    expect(executed).toBe(true);
  });
});
