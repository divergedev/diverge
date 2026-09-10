import os
import contextvars

DEFAULT_HEADER_KEY = "x-diverge-env"
BINARY_HEADER_KEY = "x-diverge-context-bin"

_current_env: contextvars.ContextVar[str] = contextvars.ContextVar("_current_env", default="")

def get_environment() -> str:
    env = _current_env.get()
    if env:
        return env
    return os.environ.get("DIVERGE_ENV", "")

def set_environment(name: str) -> contextvars.Token:
    return _current_env.set(name)

def reset_environment(token: contextvars.Token) -> None:
    _current_env.reset(token)

def get_header_key() -> str:
    return os.environ.get("DIVERGE_HEADER_KEY", DEFAULT_HEADER_KEY)
