import { describe, it, expect, beforeEach } from 'vitest';
import { getEnvironment, setEnvironment, getHeaderKey } from '../src/context.js';

describe('Context', () => {
  beforeEach(() => {
    delete process.env.DIVERGE_ENV;
    delete process.env.DIVERGE_HEADER_KEY;
  });

  it('should return empty string by default', () => {
    expect(getEnvironment()).toBe('');
  });

  it('should return from env var', () => {
    process.env.DIVERGE_ENV = 'test-env';
    expect(getEnvironment()).toBe('test-env');
  });

  it('should return from async local storage', () => {
    setEnvironment('async-env', () => {
      expect(getEnvironment()).toBe('async-env');
    });
  });

  it('should return custom header key', () => {
    expect(getHeaderKey()).toBe('x-diverge-env');
    process.env.DIVERGE_HEADER_KEY = 'x-custom-key';
    expect(getHeaderKey()).toBe('x-custom-key');
  });
});
