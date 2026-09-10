import os
from diverge_sdk.context import get_environment, set_environment, get_header_key, _current_env

def test_get_environment_default(monkeypatch):
    _current_env.set("")
    monkeypatch.delenv("DIVERGE_ENV", raising=False)
    assert get_environment() == ""

def test_get_environment_env_var(monkeypatch):
    _current_env.set("")
    monkeypatch.setenv("DIVERGE_ENV", "test-env")
    assert get_environment() == "test-env"

def test_set_environment():
    _current_env.set("")
    token = set_environment("new-env")
    assert get_environment() == "new-env"
    _current_env.reset(token)

def test_get_header_key(monkeypatch):
    monkeypatch.delenv("DIVERGE_HEADER_KEY", raising=False)
    assert get_header_key() == "x-diverge-env"
    monkeypatch.setenv("DIVERGE_HEADER_KEY", "x-custom-env")
    assert get_header_key() == "x-custom-env"
