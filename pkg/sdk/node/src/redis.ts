import { getContextEnvironment } from './context.js';

export function resolveRedisPrefix(env?: NodeJS.ProcessEnv): string {
  const envMap = env ?? process.env;
  const explicitPrefix = (envMap.DIVERGE_REDIS_PREFIX || envMap.REDIS_KEY_PREFIX || '').trim();
  if (explicitPrefix) {
    return explicitPrefix.endsWith(':') ? explicitPrefix : `${explicitPrefix}:`;
  }

  const activeEnv = (getContextEnvironment()?.trim() || envMap.DIVERGE_ENV || '').trim();

  if (activeEnv) {
    return `preview:${activeEnv}:`;
  }

  return '';
}

export function resolveRedisKey(key: string, env?: NodeJS.ProcessEnv): string {
  const prefix = resolveRedisPrefix(env);
  if (!prefix || key.startsWith(prefix)) {
    return key;
  }
  return `${prefix}${key}`;
}
