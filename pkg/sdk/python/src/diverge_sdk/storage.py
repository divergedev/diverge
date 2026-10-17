import os
from typing import Mapping, Optional

from .context import _current_env

_STORAGE_PREFIX_ENV_KEYS = (
    "DIVERGE_STORAGE_PREFIX",
    "S3_KEY_PREFIX",
    "AWS_S3_PREFIX",
    "GCS_KEY_PREFIX",
)


def resolve_storage_prefix(
    environ: Optional[Mapping[str, str]] = None,
) -> str:
    env_map = environ if environ is not None else os.environ
    for env_key in _STORAGE_PREFIX_ENV_KEYS:
        raw = env_map.get(env_key, "").strip()
        if raw:
            trimmed = raw.strip("/")
            if trimmed:
                return f"{trimmed}/"

    active_env = (
        _current_env.get().strip() or env_map.get("DIVERGE_ENV", "").strip()
    ).strip("/")
    if active_env:
        ns = (
            env_map.get("DIVERGE_NAMESPACE", "").strip()
            or env_map.get("POD_NAMESPACE", "").strip()
        ).strip("/")
        if ns:
            return f"previews/{ns}/{active_env}/"
        return f"previews/{active_env}/"

    return ""


def resolve_storage_key(
    key: str,
    environ: Optional[Mapping[str, str]] = None,
) -> str:
    prefix = resolve_storage_prefix(environ=environ)
    if not prefix or key.startswith(prefix):
        return key
    clean_key = key.lstrip("/")
    if clean_key.startswith(prefix):
        return clean_key
    return f"{prefix}{clean_key}"
