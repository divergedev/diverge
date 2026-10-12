import { DEFAULT_HEADER_KEY, getHeaderKey, setEnvironment } from './context.js';
import { getOutboundHeaders, parseBaggageEnvironment } from './kafka.js';

export interface TemporalHeaderInjectOptions {
  encode?: (val: string) => unknown;
  rawStrings?: boolean;
}

export function defaultTemporalPayloadEncode(value: string): {
  metadata: { encoding: Uint8Array };
  data: Uint8Array;
} {
  const encoder = new TextEncoder();
  return {
    metadata: {
      encoding: encoder.encode('json/plain'),
    },
    data: encoder.encode(JSON.stringify(value)),
  };
}

export function defaultTemporalPayloadDecode(val: unknown): string | undefined {
  if (val === undefined || val === null) {
    return undefined;
  }

  if (typeof val === 'string') {
    const trimmed = val.trim();
    if (trimmed.startsWith('"') && trimmed.endsWith('"')) {
      try {
        const parsed = JSON.parse(trimmed);
        if (typeof parsed === 'string') {
          return parsed;
        }
      } catch {
        // Return raw string if not valid JSON
      }
    }
    return val;
  }

  if (Buffer.isBuffer(val)) {
    return defaultTemporalPayloadDecode(val.toString('utf-8'));
  }

  if (val instanceof Uint8Array) {
    return defaultTemporalPayloadDecode(new TextDecoder().decode(val));
  }

  if (typeof val === 'object' && 'data' in (val as Record<string, unknown>)) {
    return defaultTemporalPayloadDecode((val as { data?: unknown }).data);
  }

  return undefined;
}

export function getTemporalHeaderMap(): Record<string, string> {
  return getOutboundHeaders();
}

export function injectTemporalHeaders(
  headers?: Record<string, unknown>,
  encode?: ((val: string) => unknown) | TemporalHeaderInjectOptions
): Record<string, unknown> {
  const out: Record<string, unknown> = { ...(headers ?? {}) };
  const outbound = getOutboundHeaders();
  const encodeFn = typeof encode === 'function' ? encode : encode?.encode;
  const rawStrings =
    typeof encode === 'object' && encode !== null ? Boolean(encode.rawStrings) : false;

  for (const [k, v] of Object.entries(outbound)) {
    if (encodeFn) {
      out[k] = encodeFn(v);
    } else if (rawStrings) {
      out[k] = v;
    } else {
      out[k] = defaultTemporalPayloadEncode(v);
    }
  }

  return out;
}

export function withTemporalHeaders<T>(
  headers: Record<string, unknown> | undefined,
  fn: () => T,
  decode?: (val: unknown) => string | undefined
): T {
  if (!headers) {
    return fn();
  }

  const decodeFn = decode ?? defaultTemporalPayloadDecode;
  const targetKey = getHeaderKey().toLowerCase();
  let extractedEnv: string | undefined;
  let baggageVal: string | undefined;

  for (const [k, rawVal] of Object.entries(headers)) {
    const lowerKey = k.toLowerCase();
    if (lowerKey === targetKey || lowerKey === DEFAULT_HEADER_KEY) {
      const decoded = decodeFn(rawVal)?.trim();
      if (decoded) {
        extractedEnv = decoded;
        break;
      }
    } else if (lowerKey === 'baggage') {
      baggageVal = decodeFn(rawVal);
    }
  }

  if (!extractedEnv && baggageVal) {
    extractedEnv = parseBaggageEnvironment(baggageVal);
  }

  if (extractedEnv) {
    return setEnvironment(extractedEnv, fn);
  }
  return fn();
}
