import base64
from dataclasses import dataclass, field
from enum import IntEnum
from typing import Dict

class RoutingMode(IntEnum):
    UNSPECIFIED = 0
    HEADER = 1
    SUBDOMAIN = 2
    ASYNC = 3

@dataclass
class PropagationContext:
    environment: str = ""
    routing_mode: RoutingMode = RoutingMode.UNSPECIFIED
    metadata: Dict[str, str] = field(default_factory=dict)

def _encode_varint(value: int) -> bytes:
    res = bytearray()
    while True:
        byte = value & 0x7F
        value >>= 7
        if value:
            res.append(byte | 0x80)
        else:
            res.append(byte)
            break
    return bytes(res)

def _decode_varint(buffer: bytes, offset: int) -> tuple[int, int]:
    result = 0
    shift = 0
    while True:
        if offset >= len(buffer):
            raise ValueError("Truncated varint")
        byte = buffer[offset]
        offset += 1
        result |= (byte & 0x7F) << shift
        if not (byte & 0x80):
            break
        shift += 7
    return result, offset

def _encode_string(tag: int, value: str) -> bytes:
    if not value:
        return b""
    encoded = value.encode('utf-8')
    return _encode_varint(tag) + _encode_varint(len(encoded)) + encoded

def encode_propagation_context(ctx: PropagationContext) -> str:
    out = bytearray()

    if ctx.environment:
        out.extend(_encode_string(10, ctx.environment))

    if ctx.routing_mode != RoutingMode.UNSPECIFIED:
        out.extend(_encode_varint(16))
        out.extend(_encode_varint(ctx.routing_mode.value))

    if ctx.metadata:
        for k, v in ctx.metadata.items():
            entry = bytearray()
            entry.extend(_encode_string(10, k))
            entry.extend(_encode_string(18, v))
            out.extend(_encode_varint(26))
            out.extend(_encode_varint(len(entry)))
            out.extend(entry)

    b64 = base64.standard_b64encode(out).decode('ascii').rstrip('=')
    return b64

def decode_propagation_context(encoded: str) -> PropagationContext:
    if len(encoded) > 4096:
        raise ValueError("Encoded context too large")

    pad_len = (4 - (len(encoded) % 4)) % 4
    encoded += '=' * pad_len

    try:
        buffer = base64.standard_b64decode(encoded)
    except Exception as e:
        raise ValueError(f"Invalid base64: {e}")

    ctx = PropagationContext()
    offset = 0
    length = len(buffer)

    while offset < length:
        tag, offset = _decode_varint(buffer, offset)
        field_num = tag >> 3
        wire_type = tag & 0x07

        if field_num == 1 and wire_type == 2:
            str_len, offset = _decode_varint(buffer, offset)
            ctx.environment = buffer[offset:offset+str_len].decode('utf-8')
            offset += str_len
        elif field_num == 2 and wire_type == 0:
            val, offset = _decode_varint(buffer, offset)
            try:
                ctx.routing_mode = RoutingMode(val)
            except ValueError:
                pass
        elif field_num == 3 and wire_type == 2:
            entry_len, offset = _decode_varint(buffer, offset)
            entry_end = offset + entry_len
            k = ""
            v = ""
            while offset < entry_end:
                entry_tag, offset = _decode_varint(buffer, offset)
                entry_field = entry_tag >> 3
                if entry_field == 1:
                    str_len, offset = _decode_varint(buffer, offset)
                    k = buffer[offset:offset+str_len].decode('utf-8')
                    offset += str_len
                elif entry_field == 2:
                    str_len, offset = _decode_varint(buffer, offset)
                    v = buffer[offset:offset+str_len].decode('utf-8')
                    offset += str_len
                else:
                    if (entry_tag & 7) == 0:
                        _, offset = _decode_varint(buffer, offset)
                    elif (entry_tag & 7) == 2:
                        skip_len, offset = _decode_varint(buffer, offset)
                        offset += skip_len
                    elif (entry_tag & 7) == 1:
                        offset += 8
                    elif (entry_tag & 7) == 5:
                        offset += 4
            ctx.metadata[k] = v
        else:
            if wire_type == 0:
                _, offset = _decode_varint(buffer, offset)
            elif wire_type == 2:
                skip_len, offset = _decode_varint(buffer, offset)
                offset += skip_len
            elif wire_type == 1:
                offset += 8
            elif wire_type == 5:
                offset += 4

    return ctx
