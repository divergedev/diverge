import { AsyncLocalStorage } from 'node:async_hooks';

export const DEFAULT_HEADER_KEY = 'x-diverge-env';
export const BINARY_HEADER_KEY = 'x-diverge-context-bin';

const storage = new AsyncLocalStorage<string>();

export function getEnvironment(): string {
  const env = storage.getStore();
  if (env) {
    return env;
  }
  return process.env.DIVERGE_ENV || "";
}

export function setEnvironment<T>(name: string, fn: () => T): T {
  return storage.run(name, fn);
}

export function withEnvironment(name: string): void {
  storage.enterWith(name);
}

export function getHeaderKey(): string {
  return process.env.DIVERGE_HEADER_KEY || DEFAULT_HEADER_KEY;
}
