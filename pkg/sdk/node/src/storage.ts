import { getContextEnvironment } from './context.js';

const STORAGE_PREFIX_ENV_KEYS = [
  'DIVERGE_STORAGE_PREFIX',
  'S3_KEY_PREFIX',
  'AWS_S3_PREFIX',
  'GCS_KEY_PREFIX',
] as const;

export function resolveStoragePrefix(env?: NodeJS.ProcessEnv): string {
  const envMap = env ?? process.env;
  for (const envKey of STORAGE_PREFIX_ENV_KEYS) {
    const raw = (envMap[envKey] || '').trim();
    if (raw) {
      const trimmed = raw.replace(/^\/+|\/+$/g, '');
      if (trimmed) {
        return `${trimmed}/`;
      }
    }
  }

  const activeEnv = (getContextEnvironment()?.trim() || envMap.DIVERGE_ENV || '')
    .trim()
    .replace(/^\/+|\/+$/g, '');

  if (activeEnv) {
    const normalize = (val?: string): string =>
      (val ?? '').trim().replace(/^\/+|\/+$/g, '');
    const ns =
      normalize(envMap.DIVERGE_NAMESPACE) || normalize(envMap.POD_NAMESPACE);
    if (ns) {
      return `previews/${ns}/${activeEnv}/`;
    }
    return `previews/${activeEnv}/`;
  }

  return '';
}

export function resolveStorageKey(key: string, env?: NodeJS.ProcessEnv): string {
  const prefix = resolveStoragePrefix(env);
  if (!prefix || key.startsWith(prefix)) {
    return key;
  }
  const cleanKey = key.replace(/^\/+/, '');
  if (cleanKey.startsWith(prefix)) {
    return cleanKey;
  }
  return `${prefix}${cleanKey}`;
}
