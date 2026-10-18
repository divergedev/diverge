import { readFileSync } from 'node:fs';

export const DEFAULT_SERVER_URL = 'http://diverge-server.diverge-system.svc:8080';
export const DEFAULT_TOKEN_FILE = '/etc/diverge/token/token';
export const METER_ENDPOINT_PATH = '/api/v1/agenttask/meter';

export interface LLMMeterRequest {
  requestId?: string;
  iteration?: number;
  model: string;
  tokensUsed: number;
  costUSD: number;
}

export interface LLMMeterResponse {
  allowed: boolean;
  suspended: boolean;
  reason?: string;
  totalTokensUsed: number;
  totalCostUSD: number;
  iteration?: number;
  maxIterations?: number;
  guidance?: string;
}

export interface LLMMeterOptions {
  serverUrl?: string;
  token?: string;
  tokenFile?: string;
  fetchImpl?: typeof fetch;
}

export class LLMMeterError extends Error {
  public readonly statusCode: number;
  public readonly response?: LLMMeterResponse;
  public readonly isBudgetExceeded: boolean;
  public readonly isModelDisallowed: boolean;

  constructor(message: string, statusCode: number, response?: LLMMeterResponse) {
    super(message);
    this.name = 'LLMMeterError';
    this.statusCode = statusCode;
    this.response = response;
    this.isBudgetExceeded = statusCode === 429;
    this.isModelDisallowed = statusCode === 403;
    Object.setPrototypeOf(this, LLMMeterError.prototype);
  }
}

export function resolveSandboxToken(opts?: { token?: string; tokenFile?: string }): string {
  if (opts?.token && opts.token.trim().length > 0) {
    return opts.token.trim();
  }

  const envToken = process.env.DIVERGE_MACAROON_TOKEN;
  if (envToken && envToken.trim().length > 0) {
    return envToken.trim();
  }

  const tokenFilePath = opts?.tokenFile || process.env.DIVERGE_TOKEN_FILE || DEFAULT_TOKEN_FILE;
  try {
    const fileToken = readFileSync(tokenFilePath, 'utf-8').trim();
    if (fileToken.length > 0) {
      return fileToken;
    }
  } catch (err) {
    throw new Error(
      `Failed to resolve Diverge sandbox token: no token provided via options or DIVERGE_MACAROON_TOKEN, and could not read token file at ${tokenFilePath}: ${
        err instanceof Error ? err.message : String(err)
      }`
    );
  }

  throw new Error(
    `Failed to resolve Diverge sandbox token: token file at ${tokenFilePath} is empty`
  );
}

export async function meterLLMUsage(
  req: LLMMeterRequest,
  opts?: LLMMeterOptions
): Promise<LLMMeterResponse> {
  const token = resolveSandboxToken(opts);

  const rawUrl =
    opts?.serverUrl ||
    process.env.DIVERGE_METER_URL ||
    process.env.DIVERGE_SERVER_URL ||
    DEFAULT_SERVER_URL;
  const normalizedUrl = rawUrl.replace(/\/$/, '');
  const endpoint = normalizedUrl.endsWith('/meter')
    ? normalizedUrl
    : `${normalizedUrl}${METER_ENDPOINT_PATH}`;

  const fetchFn = opts?.fetchImpl ?? fetch;
  const bodyObj: Record<string, unknown> = {
    model: req.model,
    tokensUsed: req.tokensUsed,
    costUSD: req.costUSD,
  };
  if (req.requestId) {
    bodyObj.requestId = req.requestId;
  }
  if (req.iteration !== undefined) {
    bodyObj.iteration = req.iteration;
  }

  const res = await fetchFn(endpoint, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify(bodyObj),
  });

  let data: LLMMeterResponse | undefined;
  try {
    const parsed: unknown = await res.json();
    if (parsed !== null && typeof parsed === 'object' && !Array.isArray(parsed)) {
      data = parsed as LLMMeterResponse;
    }
  } catch {
    data = undefined;
  }

  if (!res.ok) {
    throw new LLMMeterError(
      data?.reason || `Metering failed with status ${res.status}`,
      res.status,
      data
    );
  }

  if (!data || typeof data !== 'object') {
    throw new LLMMeterError('Invalid meter response payload', res.status);
  }

  return data;
}
