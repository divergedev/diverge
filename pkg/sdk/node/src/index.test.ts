import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  LLMMeterError,
  meterLLMUsage,
  resolveSandboxToken,
  resolveStorageKey,
  resolveStoragePrefix,
  setEnvironment,
} from './index.js';

describe('S3 / GCS / MinIO Object Storage Key Prefix Resolution', () => {
  const savedEnv: Record<string, string | undefined> = {};
  const envKeys = [
    'DIVERGE_ENV',
    'DIVERGE_NAMESPACE',
    'POD_NAMESPACE',
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

  it('prioritizes AsyncLocalStorage over passed DIVERGE_ENV and includes namespace when set', () => {
    process.env.DIVERGE_ENV = 'pr-1';

    expect(resolveStoragePrefix({ DIVERGE_ENV: 'pr-42' })).toBe('previews/pr-42/');
    expect(
      resolveStoragePrefix({ DIVERGE_ENV: 'pr-42', DIVERGE_NAMESPACE: 'staging' })
    ).toBe('previews/staging/pr-42/');
    expect(
      resolveStoragePrefix({
        DIVERGE_ENV: 'pr-42',
        DIVERGE_NAMESPACE: '/',
        POD_NAMESPACE: 'team-a',
      })
    ).toBe('previews/team-a/pr-42/');
    expect(resolveStoragePrefix({})).toBe('');
    expect(
      setEnvironment('pr-99', () => resolveStoragePrefix({ DIVERGE_ENV: 'pr-42' }))
    ).toBe('previews/pr-99/');
  });
});

describe('LLM Metering (meterLLMUsage & resolveSandboxToken)', () => {
  let tempDir: string;
  let savedTokenEnv: string | undefined;
  let savedTokenFileEnv: string | undefined;

  beforeEach(() => {
    tempDir = mkdtempSync(join(tmpdir(), 'diverge-node-sdk-'));
    savedTokenEnv = process.env.DIVERGE_MACAROON_TOKEN;
    savedTokenFileEnv = process.env.DIVERGE_TOKEN_FILE;
    delete process.env.DIVERGE_MACAROON_TOKEN;
    delete process.env.DIVERGE_TOKEN_FILE;
  });

  afterEach(() => {
    if (savedTokenEnv === undefined) {
      delete process.env.DIVERGE_MACAROON_TOKEN;
    } else {
      process.env.DIVERGE_MACAROON_TOKEN = savedTokenEnv;
    }
    if (savedTokenFileEnv === undefined) {
      delete process.env.DIVERGE_TOKEN_FILE;
    } else {
      process.env.DIVERGE_TOKEN_FILE = savedTokenFileEnv;
    }
    rmSync(tempDir, { recursive: true, force: true });
  });

  it('sends requestId and iteration, returns guidance, and hot-reloads DIVERGE_TOKEN_FILE across calls', async () => {
    const tokenPath = join(tempDir, 'token');
    writeFileSync(tokenPath, '  token-before-pause \n', 'utf-8');
    process.env.DIVERGE_TOKEN_FILE = tokenPath;

    expect(resolveSandboxToken()).toBe('token-before-pause');

    const recordedCalls: Array<{ url: string; auth: string; body: Record<string, unknown> }> = [];
    const fakeFetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const headers = (init?.headers ?? {}) as Record<string, string>;
      recordedCalls.push({
        url: String(input),
        auth: headers.Authorization,
        body: JSON.parse(String(init?.body ?? '{}')),
      });
      return new Response(
        JSON.stringify({
          allowed: true,
          suspended: false,
          totalTokensUsed: 2400,
          totalCostUSD: 0.08,
          iteration: 3,
          maxIterations: 15,
          guidance: 'Prefer optimistic locking over mutexes',
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } }
      );
    }) as unknown as typeof fetch;

    const resp = await meterLLMUsage(
      {
        requestId: 'req-iter-3',
        iteration: 3,
        model: 'claude-sonnet-4-6',
        tokensUsed: 1200,
        costUSD: 0.04,
      },
      {
        serverUrl: 'http://diverge-server.diverge-system.svc:8080/',
        fetchImpl: fakeFetch,
      }
    );

    expect(resp.allowed).toBe(true);
    expect(resp.iteration).toBe(3);
    expect(resp.maxIterations).toBe(15);
    expect(resp.guidance).toBe('Prefer optimistic locking over mutexes');
    expect(recordedCalls).toHaveLength(1);
    expect(recordedCalls[0]).toEqual({
      url: 'http://diverge-server.diverge-system.svc:8080/api/v1/agenttask/meter',
      auth: 'Bearer token-before-pause',
      body: {
        requestId: 'req-iter-3',
        iteration: 3,
        model: 'claude-sonnet-4-6',
        tokensUsed: 1200,
        costUSD: 0.04,
      },
    });

    // Simulate kubelet atomic symlink rotation of mounted Secret on resume
    writeFileSync(tokenPath, 'token-after-resume\n', 'utf-8');
    await meterLLMUsage(
      {
        requestId: 'req-iter-4',
        iteration: 4,
        model: 'claude-sonnet-4-6',
        tokensUsed: 900,
        costUSD: 0.03,
      },
      {
        serverUrl: 'http://diverge-server.diverge-system.svc:8080',
        fetchImpl: fakeFetch,
      }
    );
    expect(recordedCalls).toHaveLength(2);
    expect(recordedCalls[1].auth).toBe('Bearer token-after-resume');
  });

  it('throws LLMMeterError with isBudgetExceeded on 429 and isModelDisallowed on 403', async () => {
    const fetch429 = vi.fn(async () =>
      new Response(
        JSON.stringify({
          allowed: false,
          suspended: true,
          reason: 'Cost budget exceeded ($5.10 > $5.00)',
          totalTokensUsed: 95000,
          totalCostUSD: 5.1,
        }),
        { status: 429, headers: { 'Content-Type': 'application/json' } }
      )
    ) as unknown as typeof fetch;

    await expect(
      meterLLMUsage(
        { model: 'claude-sonnet-4-6', tokensUsed: 5000, costUSD: 0.25 },
        { token: 'tok', fetchImpl: fetch429 }
      )
    ).rejects.toMatchObject({
      name: 'LLMMeterError',
      statusCode: 429,
      isBudgetExceeded: true,
      isModelDisallowed: false,
      message: 'Cost budget exceeded ($5.10 > $5.00)',
    });

    const fetch403 = vi.fn(async () =>
      new Response(
        JSON.stringify({
          allowed: false,
          suspended: false,
          reason: 'Model o3-pro is not allowed by SandboxTemplate policy',
          totalTokensUsed: 1000,
          totalCostUSD: 0.05,
        }),
        { status: 403, headers: { 'Content-Type': 'application/json' } }
      )
    ) as unknown as typeof fetch;

    try {
      await meterLLMUsage(
        { model: 'o3-pro', tokensUsed: 1000, costUSD: 0.1 },
        { token: 'tok', fetchImpl: fetch403 }
      );
      expect.unreachable('should have thrown LLMMeterError');
    } catch (err) {
      expect(err).toBeInstanceOf(LLMMeterError);
      const meterErr = err as LLMMeterError;
      expect(meterErr.statusCode).toBe(403);
      expect(meterErr.isModelDisallowed).toBe(true);
      expect(meterErr.isBudgetExceeded).toBe(false);
    }
  });
});
