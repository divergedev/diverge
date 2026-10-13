from contextlib import contextmanager
import os
import re
from typing import Any, Dict, Iterator, List, Mapping, Optional, Tuple, Union

from .context import (
    DEFAULT_HEADER_KEY,
    _current_env,
    get_environment,
    get_header_key,
    reset_environment,
    set_environment,
)


def _to_bytes(val: Any) -> bytes:
    if isinstance(val, bytes):
        return val
    if isinstance(val, bytearray):
        return bytes(val)
    return str(val).encode("utf-8")


def _to_str(val: Any) -> Optional[str]:
    if val is None:
        return None
    if isinstance(val, str):
        return val
    if isinstance(val, (bytes, bytearray)):
        return bytes(val).decode("utf-8", errors="replace")
    if isinstance(val, (list, tuple)) and len(val) > 0:
        return _to_str(val[0])
    return str(val)


def parse_baggage_environment(baggage: str) -> Optional[str]:
    for part in baggage.split(","):
        trimmed = part.strip()
        if not trimmed:
            continue
        kv_part = trimmed.split(";", 1)[0]
        if "=" not in kv_part:
            continue
        k, v = kv_part.split("=", 1)
        key = k.strip().lower()
        val = v.strip()
        if key in ("diverge-env", DEFAULT_HEADER_KEY) and val:
            return val
    return None


def inject_kafka_headers(
    headers: Optional[Union[Dict[str, bytes], List[Tuple[str, bytes]]]] = None,
) -> List[Tuple[str, bytes]]:
    result: List[Tuple[str, bytes]] = []
    if headers is not None:
        if isinstance(headers, Mapping):
            for k, v in headers.items():
                result.append((str(k), _to_bytes(v)))
        else:
            for k, v in headers:
                result.append((str(k), _to_bytes(v)))

    env = get_environment()
    if not env:
        return result

    header_key = get_header_key()
    lower_keys_to_replace = {header_key.lower(), "baggage"}
    result = [(k, v) for k, v in result if k.lower() not in lower_keys_to_replace]
    result.append((header_key, env.encode("utf-8")))
    result.append(("baggage", f"diverge-env={env}".encode("utf-8")))
    return result


def extract_kafka_context(headers: Any) -> Optional[str]:
    if headers is None:
        return None

    if not isinstance(headers, (Mapping, list, tuple)) and hasattr(headers, "headers"):
        hdr_attr = getattr(headers, "headers")
        headers = hdr_attr() if callable(hdr_attr) else hdr_attr
        if headers is None:
            return None

    items: List[Tuple[Any, Any]] = []
    if isinstance(headers, Mapping):
        items = list(headers.items())
    else:
        try:
            items = list(headers)
        except TypeError:
            return None

    target_key = get_header_key().lower()
    baggage_val: Optional[str] = None

    for k, raw_val in items:
        key_str = (_to_str(k) or "").lower()
        if key_str in (target_key, DEFAULT_HEADER_KEY):
            decoded = (_to_str(raw_val) or "").strip()
            if decoded:
                return decoded
        elif key_str == "baggage":
            baggage_val = _to_str(raw_val)

    if baggage_val:
        return parse_baggage_environment(baggage_val)

    return None


@contextmanager
def kafka_message_context(headers: Any) -> Iterator[Optional[str]]:
    env = extract_kafka_context(headers)
    token = set_environment(env) if env else None
    try:
        yield env
    finally:
        if token is not None:
            reset_environment(token)


def resolve_kafka_topic(
    base_topic: str,
    environ: Optional[Mapping[str, str]] = None,
) -> str:
    env_map = environ if environ is not None else os.environ
    upper_snake = re.sub(r"[^A-Z0-9]", "_", base_topic.upper())
    env_var_key = f"DIVERGE_KAFKA_TOPIC_{upper_snake}"
    mapped = env_map.get(env_var_key, "").strip()
    if mapped:
        return mapped

    active_env = _current_env.get().strip() or env_map.get("DIVERGE_ENV", "").strip()
    if active_env:
        return f"{base_topic}.preview.{active_env}"

    return base_topic
