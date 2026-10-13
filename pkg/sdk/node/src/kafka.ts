import { DEFAULT_HEADER_KEY, getEnvironment, getHeaderKey, setEnvironment } from './context.js';

export type KafkaHeaders = Record<
  string,
  string | Buffer | Uint8Array | Array<string | Buffer | Uint8Array> | undefined
>;

export function getOutboundHeaders(): Record<string, string> {
  const env = getEnvironment();
  if (!env) {
    return {};
  }
  return {
    [getHeaderKey()]: env,
    baggage: `diverge-env=${env}`,
  };
}

export function injectKafkaHeaders<
  T extends Record<string, unknown> = Record<string, string>,
>(headers?: T): T & Record<string, string> {
  const outbound = getOutboundHeaders();
  const outboundKeys = Object.keys(outbound);
  if (outboundKeys.length === 0) {
    return {
      ...(headers ?? ({} as T)),
    } as T & Record<string, string>;
  }

  const blockedKeys = new Set<string>([
    DEFAULT_HEADER_KEY.toLowerCase(),
    ...outboundKeys.map((k) => k.toLowerCase()),
  ]);

  const cleaned: Record<string, unknown> = {};
  if (headers) {
    for (const [k, v] of Object.entries(headers)) {
      if (!blockedKeys.has(k.toLowerCase())) {
        cleaned[k] = v;
      }
    }
  }

  return {
    ...cleaned,
    ...outbound,
  } as T & Record<string, string>;
}

export function extractKafkaHeaderValue(val: unknown): string | undefined {
  if (val === undefined || val === null) {
    return undefined;
  }
  if (typeof val === 'string') {
    return val;
  }
  if (Buffer.isBuffer(val)) {
    return val.toString('utf-8');
  }
  if (val instanceof Uint8Array) {
    return new TextDecoder().decode(val);
  }
  if (Array.isArray(val)) {
    if (val.length === 0) {
      return undefined;
    }
    return extractKafkaHeaderValue(val[0]);
  }
  return undefined;
}

export function parseBaggageEnvironment(baggage: string): string | undefined {
  for (const part of baggage.split(',')) {
    const trimmed = part.trim();
    if (!trimmed) {
      continue;
    }
    const [kvPart] = trimmed.split(';');
    const eqIdx = kvPart.indexOf('=');
    if (eqIdx === -1) {
      continue;
    }
    const key = kvPart.slice(0, eqIdx).trim().toLowerCase();
    const val = kvPart.slice(eqIdx + 1).trim();
    if ((key === 'diverge-env' || key === DEFAULT_HEADER_KEY) && val) {
      return val;
    }
  }
  return undefined;
}

export function withKafkaMessage<T>(
  message: { headers?: Record<string, unknown> },
  fn: () => T
): T {
  const headers = message?.headers;
  if (!headers) {
    return fn();
  }

  const targetKey = getHeaderKey().toLowerCase();
  let extractedEnv: string | undefined;
  let baggageVal: string | undefined;

  for (const [k, rawVal] of Object.entries(headers)) {
    const lowerKey = k.toLowerCase();
    if (lowerKey === targetKey || lowerKey === DEFAULT_HEADER_KEY) {
      const decoded = extractKafkaHeaderValue(rawVal)?.trim();
      if (decoded) {
        extractedEnv = decoded;
        break;
      }
    } else if (lowerKey === 'baggage') {
      baggageVal = extractKafkaHeaderValue(rawVal);
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

export function resolveKafkaTopic(baseTopic: string, env?: NodeJS.ProcessEnv): string {
  const envMap = env ?? process.env;
  const envVarKey =
    'DIVERGE_KAFKA_TOPIC_' + baseTopic.toUpperCase().replace(/[^A-Z0-9]/g, '_');
  const mappedTopic = envMap[envVarKey]?.trim();
  if (mappedTopic) {
    return mappedTopic;
  }

  const contextEnv = getEnvironment();
  const processDefaultEnv = process.env.DIVERGE_ENV || '';
  const hasExplicitContextEnv = contextEnv !== '' && contextEnv !== processDefaultEnv;
  const activeEnv = (
    hasExplicitContextEnv
      ? contextEnv
      : envMap.DIVERGE_ENV?.trim() || contextEnv
  ).trim();

  if (activeEnv) {
    return `${baseTopic}.preview.${activeEnv}`;
  }

  return baseTopic;
}
