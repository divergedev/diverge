from contextlib import contextmanager
import json
from typing import Any, Dict, Iterator, Mapping, Optional

from .context import (
    DEFAULT_HEADER_KEY,
    get_environment,
    get_header_key,
    reset_environment,
    set_environment,
)
from .kafka import parse_baggage_environment


def _decode_temporal_value(val: Any) -> Optional[str]:
    if val is None:
        return None

    if hasattr(val, "data") and not isinstance(val, (bytes, bytearray, str, Mapping)):
        return _decode_temporal_value(getattr(val, "data"))

    if isinstance(val, Mapping) and "data" in val:
        return _decode_temporal_value(val["data"])

    if isinstance(val, (bytes, bytearray)):
        return _decode_temporal_value(bytes(val).decode("utf-8", errors="replace"))

    if isinstance(val, str):
        trimmed = val.strip()
        if trimmed.startswith('"') and trimmed.endswith('"'):
            try:
                parsed = json.loads(trimmed)
                if isinstance(parsed, str):
                    return parsed
            except ValueError:
                pass
        return val

    return None


def get_temporal_headers() -> Dict[str, str]:
    env = get_environment()
    if not env:
        return {}
    return {
        get_header_key(): env,
        "baggage": f"diverge-env={env}",
    }


@contextmanager
def temporal_header_context(
    headers: Optional[Mapping[str, Any]],
) -> Iterator[Optional[str]]:
    extracted_env: Optional[str] = None
    if headers:
        target_key = get_header_key().lower()
        baggage_val: Optional[str] = None

        for k, raw_val in headers.items():
            lower_key = str(k).lower()
            if lower_key in (target_key, DEFAULT_HEADER_KEY):
                decoded = (_decode_temporal_value(raw_val) or "").strip()
                if decoded:
                    extracted_env = decoded
                    break
            elif lower_key == "baggage":
                baggage_val = _decode_temporal_value(raw_val)

        if not extracted_env and baggage_val:
            extracted_env = parse_baggage_environment(baggage_val)

    token = set_environment(extracted_env) if extracted_env else None
    try:
        yield extracted_env
    finally:
        if token is not None:
            reset_environment(token)
