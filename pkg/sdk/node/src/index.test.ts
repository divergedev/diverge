import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import {
  resolveStorageKey,
  resolveStoragePrefix,
  setEnvironment,
} from './index.js';

describe('S3 / GCS / MinIO Object Storage Key Prefix Resolution', () => {
  const savedEnv: Record<string, string | undefined> = {};
  const envKeys = [
    'DIVERGE_ENV',
    'DIVERGE_STORAGE_PREFIX',
    'S3_KEY_PREFIX',
    'AWS_S3_PREFIX',
    'GCS_KEY_PREFIX',
  ];

  beforeEach(() => {
    for (const key of envKeys) {
      savedEnv[key] = process.env[key];
      delete process.env[key];
    }
  });

  afterEach(() => {
    for (const [key, val] of Object.entries(savedEnv)) {
      if (val === undefined) {
        delete process.env[key];
      } else {
        process.env[key] = val;
      }
    }
  });

  it('returns empty prefix and unmodified key in baseline', () => {
    expect(resolveStoragePrefix()).toBe('');
    expect(resolveStorageKey('avatars/user-123.png')).toBe('avatars/user-123.png');
    expect(resolveStorageKey('/avatars/user-123.png')).toBe('/avatars/user-123.png');
  });

  it('derives previews/<env>/ prefix from active context and prefixes keys idempotently', () => {
    setEnvironment('pr-42', () => {
      expect(resolveStoragePrefix()).toBe('previews/pr-42/');
      const prefixed = resolveStorageKey('avatars/user-123.png');
      expect(prefixed).toBe('previews/pr-42/avatars/user-123.png');
      expect(resolveStorageKey(prefixed)).toBe('previews/pr-42/avatars/user-123.png');
      expect(resolveStorageKey('/avatars/user-123.png')).toBe(
        'previews/pr-42/avatars/user-123.png'
      );
      expect(resolveStorageKey('/previews/pr-42/avatars/user-123.png')).toBe(
        'previews/pr-42/avatars/user-123.png'
      );
    });
  });

  it('respects explicit storage prefix env vars and normalizes slashes', () => {
    expect(
      resolveStoragePrefix({ DIVERGE_STORAGE_PREFIX: '/previews/pr-42/uploads' })
    ).toBe('previews/pr-42/uploads/');
    expect(
      resolveStorageKey('/report.pdf', {
        DIVERGE_STORAGE_PREFIX: 'previews/pr-42/uploads',
      })
    ).toBe('previews/pr-42/uploads/report.pdf');
    expect(
      resolveStorageKey('previews/pr-42/uploads/report.pdf', {
        DIVERGE_STORAGE_PREFIX: 'previews/pr-42/uploads/',
      })
    ).toBe('previews/pr-42/uploads/report.pdf');
    expect(resolveStoragePrefix({ S3_KEY_PREFIX: 'previews/pr-55/s3' })).toBe(
      'previews/pr-55/s3/'
    );
    expect(resolveStoragePrefix({ AWS_S3_PREFIX: '/previews/pr-66/aws/' })).toBe(
      'previews/pr-66/aws/'
    );
    expect(resolveStoragePrefix({ GCS_KEY_PREFIX: 'previews/pr-77/gcs/' })).toBe(
      'previews/pr-77/gcs/'
    );
  });

  it('prioritizes AsyncLocalStorage over passed DIVERGE_ENV', () => {
    process.env.DIVERGE_ENV = 'pr-1';

    expect(resolveStoragePrefix({ DIVERGE_ENV: 'pr-42' })).toBe('previews/pr-42/');
    expect(resolveStoragePrefix({})).toBe('');
    expect(
      setEnvironment('pr-99', () => resolveStoragePrefix({ DIVERGE_ENV: 'pr-42' }))
    ).toBe('previews/pr-99/');
  });
});
