import os
from typing import Mapping, Optional

from .context import _current_env


def resolve_redis_prefix(
    environ: Optional[Mapping[str, str]] = None,
) -> str:
    env_map = environ if environ is not None else os.environ
    explicit_prefix = (
        env_map.get("DIVERGE_REDIS_PREFIX", "").strip()
        or env_map.get("REDIS_KEY_PREFIX", "").strip()
    )
    if explicit_prefix:
        return explicit_prefix if explicit_prefix.endswith(":") else f"{explicit_prefix}:"

    active_env = _current_env.get().strip() or env_map.get("DIVERGE_ENV", "").strip()
    if active_env:
        return f"preview:{active_env}:"

    return ""


def resolve_redis_key(
    key: str,
    environ: Optional[Mapping[str, str]] = None,
) -> str:
    prefix = resolve_redis_prefix(environ=environ)
    if not prefix or key.startswith(prefix):
        return key
    return f"{prefix}{key}"
