export enum RoutingMode {
  UNSPECIFIED = 0,
  HEADER = 1,
  SUBDOMAIN = 2,
  ASYNC = 3
}

export interface PropagationContext {
  environment: string;
  routing_mode: RoutingMode;
  metadata: Record<string, string>;
}

function encodeVarint(value: number): Buffer {
  const res: number[] = [];
  while (true) {
    const byte = value & 0x7F;
    value >>= 7;
    if (value) {
      res.push(byte | 0x80);
    } else {
      res.push(byte);
      break;
    }
  }
  return Buffer.from(res);
}

function decodeVarint(buffer: Buffer, offset: number): { value: number, offset: number } {
  let result = 0;
  let shift = 0;
  let bytesRead = 0;
  while (true) {
    if (offset >= buffer.length) {
      throw new Error("Truncated varint");
    }
    if (bytesRead >= 5) {
      throw new Error("Varint too long");
    }
    const byte = buffer[offset];
    offset++;
    bytesRead++;
    result = (result + ((byte & 0x7F) * Math.pow(2, shift))) >>> 0;
    if (!(byte & 0x80)) {
      break;
    }
    shift += 7;
  }
  return { value: result, offset };
}

function encodeString(tag: number, value: string): Buffer {
  if (!value) {
    return Buffer.alloc(0);
  }
  const encoded = Buffer.from(value, 'utf-8');
  return Buffer.concat([encodeVarint(tag), encodeVarint(encoded.length), encoded]);
}

export function encodePropagationContext(ctx: PropagationContext): string {
  const chunks: Buffer[] = [];

  if (ctx.environment) {
    chunks.push(encodeString(10, ctx.environment));
  }

  if (ctx.routing_mode !== RoutingMode.UNSPECIFIED && ctx.routing_mode !== undefined) {
    chunks.push(encodeVarint(16));
    chunks.push(encodeVarint(ctx.routing_mode));
  }

  if (ctx.metadata) {
    for (const [k, v] of Object.entries(ctx.metadata)) {
      const kBuf = encodeString(10, k);
      const vBuf = encodeString(18, v);
      const entry = Buffer.concat([kBuf, vBuf]);
      chunks.push(encodeVarint(26));
      chunks.push(encodeVarint(entry.length));
      chunks.push(entry);
    }
  }

  const buf = Buffer.concat(chunks);
  return buf.toString('base64').replace(/=+$/, '');
}

export function decodePropagationContext(encoded: string): PropagationContext {
  if (encoded.length > 4096) {
    throw new Error("Encoded context too large");
  }

  // Pad if necessary
  const padLen = (4 - (encoded.length % 4)) % 4;
  encoded += '='.repeat(padLen);

  const buffer = Buffer.from(encoded, 'base64');

  const ctx: PropagationContext = {
    environment: "",
    routing_mode: RoutingMode.UNSPECIFIED,
    metadata: {}
  };

  let offset = 0;

  while (offset < buffer.length) {
    let { value: tag, offset: nextOffset } = decodeVarint(buffer, offset);
    offset = nextOffset;

    const fieldNum = tag >> 3;
    const wireType = tag & 0x07;

    if (fieldNum === 1 && wireType === 2) {
      const { value: strLen, offset: next } = decodeVarint(buffer, offset);
      if (strLen < 0 || next + strLen > buffer.length) {
        throw new Error("Invalid field length");
      }
      ctx.environment = buffer.toString('utf-8', next, next + strLen);
      offset = next + strLen;
    } else if (fieldNum === 2 && wireType === 0) {
      const { value, offset: next } = decodeVarint(buffer, offset);
      ctx.routing_mode = value as RoutingMode;
      offset = next;
    } else if (fieldNum === 3 && wireType === 2) {
      const { value: entryLen, offset: next } = decodeVarint(buffer, offset);
      if (entryLen < 0 || next + entryLen > buffer.length) {
        throw new Error("Invalid field length");
      }
      offset = next;
      const entryEnd = offset + entryLen;
      let k = "";
      let v = "";
      while (offset < entryEnd) {
        const { value: entryTag, offset: eNext } = decodeVarint(buffer, offset);
        offset = eNext;
        const entryField = entryTag >> 3;

        if (entryField === 1) {
          const { value: strLen, offset: next2 } = decodeVarint(buffer, offset);
          if (strLen < 0 || next2 + strLen > buffer.length) {
            throw new Error("Invalid field length");
          }
          k = buffer.toString('utf-8', next2, next2 + strLen);
          offset = next2 + strLen;
        } else if (entryField === 2) {
          const { value: strLen, offset: next2 } = decodeVarint(buffer, offset);
          if (strLen < 0 || next2 + strLen > buffer.length) {
            throw new Error("Invalid field length");
          }
          v = buffer.toString('utf-8', next2, next2 + strLen);
          offset = next2 + strLen;
        } else {
          // Skip unknown field in entry
          const et = entryTag & 7;
          if (et === 0) {
            const { offset: next2 } = decodeVarint(buffer, offset);
            offset = next2;
          } else if (et === 2) {
            const { value: skipLen, offset: next2 } = decodeVarint(buffer, offset);
            if (skipLen < 0 || next2 + skipLen > buffer.length) {
              throw new Error("Invalid field length");
            }
            offset = next2 + skipLen;
          } else if (et === 1) {
            offset += 8;
          } else if (et === 5) {
            offset += 4;
          }
        }
      }
      ctx.metadata[k] = v;
    } else {
      // Skip unknown field
      if (wireType === 0) {
        const { offset: next } = decodeVarint(buffer, offset);
        offset = next;
      } else if (wireType === 2) {
        const { value: skipLen, offset: next } = decodeVarint(buffer, offset);
        if (skipLen < 0 || next + skipLen > buffer.length) {
          throw new Error("Invalid field length");
        }
        offset = next + skipLen;
      } else if (wireType === 1) {
        offset += 8;
      } else if (wireType === 5) {
        offset += 4;
      }
    }
  }

  return ctx;
}
