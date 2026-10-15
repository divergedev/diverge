import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import {
  getEnvironment,
  getTemporalHeaderMap,
  injectKafkaHeaders,
  injectTemporalHeaders,
  LLMMeterError,
  meterLLMUsage,
  resolveKafkaTopic,
  resolveSandboxToken,
  setEnvironment,
  withKafkaMessage,
  withTemporalHeaders,
} from '../src/index.js';

describe('LLM Usage Metering', () => {
  beforeEach(() => {
    delete process.env.DIVERGE_MACAROON_TOKEN;
    delete process.env.DIVERGE_TOKEN_FILE;
    delete process.env.DIVERGE_METER_URL;
    delete process.env.DIVERGE_SERVER_URL;
  });

  it('meters usage on 200 OK', async () => {
    let capturedUrl = '';
    let capturedInit: RequestInit | undefined;

    const mockFetch: typeof fetch = async (input, init) => {
      capturedUrl = String(input);
      capturedInit = init;
      return new Response(
        JSON.stringify({
          allowed: true,
          suspended: false,
          totalTokensUsed: 1500,
          totalCostUSD: 0.045,
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } }
      );
    };

    const res = await meterLLMUsage(
      { model: 'claude-sonnet-4-6', tokensUsed: 500, costUSD: 0.015 },
      {
        serverUrl: 'http://localhost:8080/',
        token: 'macaroon-test-token',
        fetchImpl: mockFetch,
      }
    );

    expect(capturedUrl).toBe('http://localhost:8080/api/v1/agenttask/meter');
    expect(capturedInit?.method).toBe('POST');
    expect((capturedInit?.headers as Record<string, string>)?.Authorization).toBe(
      'Bearer macaroon-test-token'
    );
    expect(JSON.parse(String(capturedInit?.body))).toEqual({
      model: 'claude-sonnet-4-6',
      tokensUsed: 500,
      costUSD: 0.015,
    });
    expect(res).toEqual({
      allowed: true,
      suspended: false,
      totalTokensUsed: 1500,
      totalCostUSD: 0.045,
    });
  });

  it('throws LLMMeterError with isBudgetExceeded on 429', async () => {
    const mockFetch: typeof fetch = async () =>
      new Response(
        JSON.stringify({
          allowed: false,
          suspended: true,
          reason: 'Token budget exceeded (12000 > 10000)',
          totalTokensUsed: 12000,
          totalCostUSD: 0.36,
        }),
        { status: 429, headers: { 'Content-Type': 'application/json' } }
      );

    await expect(
      meterLLMUsage(
        { model: 'claude-sonnet-4-6', tokensUsed: 3000, costUSD: 0.09 },
        { token: 'tok-123', fetchImpl: mockFetch }
      )
    ).rejects.toSatisfy((err: unknown) => {
      expect(err).toBeInstanceOf(LLMMeterError);
      const meterErr = err as LLMMeterError;
      expect(meterErr.statusCode).toBe(429);
      expect(meterErr.isBudgetExceeded).toBe(true);
      expect(meterErr.isModelDisallowed).toBe(false);
      expect(meterErr.message).toContain('Token budget exceeded');
      expect(meterErr.response?.suspended).toBe(true);
      return true;
    });
  });

  it('throws LLMMeterError with isModelDisallowed on 403', async () => {
    const mockFetch: typeof fetch = async () =>
      new Response(
        JSON.stringify({
          allowed: false,
          suspended: false,
          reason: 'Model gpt-4.5-preview is not in allowedModels',
          totalTokensUsed: 100,
          totalCostUSD: 0.003,
        }),
        { status: 403, headers: { 'Content-Type': 'application/json' } }
      );

    await expect(
      meterLLMUsage(
        { model: 'gpt-4.5-preview', tokensUsed: 200, costUSD: 0.01 },
        { token: 'tok-123', fetchImpl: mockFetch }
      )
    ).rejects.toSatisfy((err: unknown) => {
      expect(err).toBeInstanceOf(LLMMeterError);
      const meterErr = err as LLMMeterError;
      expect(meterErr.statusCode).toBe(403);
      expect(meterErr.isModelDisallowed).toBe(true);
      expect(meterErr.isBudgetExceeded).toBe(false);
      expect(meterErr.message).toContain('not in allowedModels');
      return true;
    });
  });

  it('loads token from file when opts.tokenFile or DIVERGE_TOKEN_FILE is configured', () => {
    const tempDir = mkdtempSync(join(tmpdir(), 'diverge-sdk-node-test-'));
    try {
      const tokenPath = join(tempDir, 'token');
      writeFileSync(tokenPath, '  file-macaroon-token-xyz \n', 'utf-8');
      expect(resolveSandboxToken({ tokenFile: tokenPath })).toBe('file-macaroon-token-xyz');

      process.env.DIVERGE_TOKEN_FILE = tokenPath;
      expect(resolveSandboxToken()).toBe('file-macaroon-token-xyz');
    } finally {
      rmSync(tempDir, { recursive: true, force: true });
    }
  });

  it('throws LLMMeterError when a 2xx response contains malformed or non-object JSON', async () => {
    const mockFetch: typeof fetch = async () =>
      new Response('not-json', { status: 200, headers: { 'Content-Type': 'application/json' } });

    await expect(
      meterLLMUsage(
        { model: 'claude-sonnet-4-6', tokensUsed: 100, costUSD: 0.003 },
        { token: 'tok-123', fetchImpl: mockFetch }
      )
    ).rejects.toSatisfy((err: unknown) => {
      expect(err).toBeInstanceOf(LLMMeterError);
      const meterErr = err as LLMMeterError;
      expect(meterErr.statusCode).toBe(200);
      expect(meterErr.message).toBe('Invalid meter response payload');
      return true;
    });
  });
});

describe('Kafka Context Propagation & Topic Resolution', () => {
  beforeEach(() => {
    delete process.env.DIVERGE_ENV;
    delete process.env.DIVERGE_HEADER_KEY;
    delete process.env.DIVERGE_KAFKA_TOPIC_ORDERS_EVENTS;
  });

  it('injects and extracts Kafka headers across message boundaries', () => {
    const outboundHeaders = setEnvironment('pr-42', () =>
      injectKafkaHeaders({ 'x-trace-id': 'abc', 'X-Diverge-Env': 'stale-env', Baggage: 'diverge-env=stale-env' })
    );

    expect(outboundHeaders['x-trace-id']).toBe('abc');
    expect(outboundHeaders['x-diverge-env']).toBe('pr-42');
    expect(outboundHeaders['X-Diverge-Env']).toBeUndefined();
    expect(outboundHeaders.baggage).toBe('diverge-env=pr-42');
    expect(outboundHeaders.Baggage).toBeUndefined();

    const observed = withKafkaMessage(
      {
        headers: {
          'x-diverge-env': Buffer.from('pr-42', 'utf-8'),
        },
      },
      () => getEnvironment()
    );
    expect(observed).toBe('pr-42');

    const observedFromBaggage = withKafkaMessage(
      {
        headers: {
          baggage: new TextEncoder().encode('other=1, diverge-env=pr-99'),
        },
      },
      () => getEnvironment()
    );
    expect(observedFromBaggage).toBe('pr-99');
  });

  it('resolves Kafka topics from env mapping or active preview context', () => {
    expect(resolveKafkaTopic('orders.events')).toBe('orders.events');

    const previewTopic = setEnvironment('pr-42', () => resolveKafkaTopic('orders.events'));
    expect(previewTopic).toBe('orders.events.preview.pr-42');

    process.env.DIVERGE_KAFKA_TOPIC_ORDERS_EVENTS = 'orders.events.custom-branch';
    expect(setEnvironment('pr-42', () => resolveKafkaTopic('orders.events'))).toBe(
      'orders.events.custom-branch'
    );
  });
});

describe('Temporal Header Propagation', () => {
  beforeEach(() => {
    delete process.env.DIVERGE_ENV;
    delete process.env.DIVERGE_HEADER_KEY;
  });

  it('propagates environment via getTemporalHeaderMap, injectTemporalHeaders, and withTemporalHeaders', () => {
    const headerMap = setEnvironment('agent-sbx-7', () => getTemporalHeaderMap());
    expect(headerMap).toEqual({
      'x-diverge-env': 'agent-sbx-7',
      baggage: 'diverge-env=agent-sbx-7',
    });

    const encodedHeaders = setEnvironment('agent-sbx-7', () =>
      injectTemporalHeaders({ existing: 'header' })
    );
    expect(encodedHeaders.existing).toBe('header');

    const roundTripEnv = withTemporalHeaders(encodedHeaders, () => getEnvironment());
    expect(roundTripEnv).toBe('agent-sbx-7');

    const rawHeaders = setEnvironment('agent-sbx-8', () =>
      injectTemporalHeaders({}, { rawStrings: true })
    );
    expect(rawHeaders['x-diverge-env']).toBe('agent-sbx-8');
    expect(withTemporalHeaders(rawHeaders, () => getEnvironment())).toBe('agent-sbx-8');
  });
});

describe('Redis / Valkey Key Prefix Resolution', () => {
  const savedEnv: Record<string, string | undefined> = {};

  beforeEach(() => {
    for (const key of ['DIVERGE_ENV', 'DIVERGE_REDIS_PREFIX', 'REDIS_KEY_PREFIX']) {
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

  it('returns empty prefix and unmodified key in baseline', async () => {
    const { resolveRedisPrefix, resolveRedisKey } = await import('../src/index.js');
    expect(resolveRedisPrefix()).toBe('');
    expect(resolveRedisKey('session:user-123')).toBe('session:user-123');
  });

  it('derives preview:<env>: prefix from active context and prefixes keys idempotently', async () => {
    const { resolveRedisPrefix, resolveRedisKey } = await import('../src/index.js');
    setEnvironment('pr-42', () => {
      expect(resolveRedisPrefix()).toBe('preview:pr-42:');
      const prefixed = resolveRedisKey('session:user-123');
      expect(prefixed).toBe('preview:pr-42:session:user-123');
      expect(resolveRedisKey(prefixed)).toBe('preview:pr-42:session:user-123');
    });
  });

  it('respects DIVERGE_REDIS_PREFIX and REDIS_KEY_PREFIX and ensures trailing colon', async () => {
    const { resolveRedisPrefix, resolveRedisKey } = await import('../src/index.js');
    expect(resolveRedisPrefix({ DIVERGE_REDIS_PREFIX: 'preview:pr-42:cache' })).toBe(
      'preview:pr-42:cache:'
    );
    expect(
      resolveRedisKey('user:1', { DIVERGE_REDIS_PREFIX: 'preview:pr-42:cache' })
    ).toBe('preview:pr-42:cache:user:1');
    expect(
      resolveRedisKey('preview:pr-42:cache:user:1', {
        DIVERGE_REDIS_PREFIX: 'preview:pr-42:cache:',
      })
    ).toBe('preview:pr-42:cache:user:1');
    expect(resolveRedisPrefix({ REDIS_KEY_PREFIX: 'preview:pr-77:' })).toBe(
      'preview:pr-77:'
    );
  });
});

