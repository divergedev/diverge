from dataclasses import dataclass
from http.server import BaseHTTPRequestHandler, HTTPServer
import json
import threading
from typing import Any, Dict, List
import pytest

from diverge_sdk import (
    LLMMeterError,
    extract_kafka_context,
    get_environment,
    get_temporal_headers,
    inject_kafka_headers,
    kafka_message_context,
    meter_llm_usage,
    reset_environment,
    resolve_kafka_topic,
    resolve_sandbox_token,
    set_environment,
    temporal_header_context,
)
from diverge_sdk.context import _current_env


class _MockMeterHandler(BaseHTTPRequestHandler):
    status_code: int = 200
    response_body: Dict[str, Any] = {}
    raw_response_bytes: bytes = b""
    recorded_requests: List[Dict[str, Any]] = []

    def do_POST(self) -> None:
        length = int(self.headers.get("Content-Length", "0"))
        raw = self.rfile.read(length).decode("utf-8") if length > 0 else "{}"
        self.__class__.recorded_requests.append(
            {
                "path": self.path,
                "auth": self.headers.get("Authorization"),
                "body": json.loads(raw),
            }
        )
        if self.__class__.raw_response_bytes:
            payload = self.__class__.raw_response_bytes
        else:
            payload = json.dumps(self.__class__.response_body).encode("utf-8")
        self.send_response(self.__class__.status_code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def log_message(self, format: str, *args: Any) -> None:
        return


@pytest.fixture
def meter_server():
    _MockMeterHandler.recorded_requests = []
    _MockMeterHandler.status_code = 200
    _MockMeterHandler.raw_response_bytes = b""
    _MockMeterHandler.response_body = {
        "allowed": True,
        "suspended": False,
        "totalTokensUsed": 1500,
        "totalCostUSD": 0.045,
    }
    server = HTTPServer(("127.0.0.1", 0), _MockMeterHandler)
    port = server.server_address[1]
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield f"http://127.0.0.1:{port}", _MockMeterHandler
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2.0)


def test_meter_llm_usage_200(meter_server, monkeypatch):
    base_url, handler = meter_server
    monkeypatch.delenv("DIVERGE_MACAROON_TOKEN", raising=False)
    monkeypatch.delenv("DIVERGE_TOKEN_FILE", raising=False)

    resp = meter_llm_usage(
        "claude-sonnet-4-6",
        500,
        0.015,
        server_url=base_url,
        token="test",
    )

    assert resp.allowed is True
    assert resp.suspended is False
    assert resp.total_tokens_used == 1500
    assert resp.total_cost_usd == pytest.approx(0.045)
    assert len(handler.recorded_requests) == 1
    assert handler.recorded_requests[0]["path"] == "/api/v1/agenttask/meter"
    assert handler.recorded_requests[0]["auth"] == "Bearer test"
    assert handler.recorded_requests[0]["body"] == {
        "model": "claude-sonnet-4-6",
        "tokensUsed": 500,
        "costUSD": 0.015,
    }


def test_meter_llm_usage_429_budget_exceeded(meter_server):
    base_url, handler = meter_server
    handler.status_code = 429
    handler.response_body = {
        "allowed": False,
        "suspended": True,
        "reason": "Cost budget exceeded ($1.20 > $1.00)",
        "totalTokensUsed": 40000,
        "totalCostUSD": 1.20,
    }

    with pytest.raises(LLMMeterError) as exc_info:
        meter_llm_usage(
            "claude-sonnet-4-6",
            5000,
            0.25,
            server_url=base_url,
            token="test",
        )

    err = exc_info.value
    assert err.status_code == 429
    assert err.is_budget_exceeded is True
    assert err.is_model_disallowed is False
    assert "Cost budget exceeded" in str(err)
    assert err.response is not None
    assert err.response.suspended is True


def test_meter_llm_usage_403_model_disallowed(meter_server):
    base_url, handler = meter_server
    handler.status_code = 403
    handler.response_body = {
        "allowed": False,
        "suspended": False,
        "reason": "Model o3-pro is not allowed by SandboxTemplate policy",
        "totalTokensUsed": 0,
        "totalCostUSD": 0.0,
    }

    with pytest.raises(LLMMeterError) as exc_info:
        meter_llm_usage(
            "o3-pro",
            1000,
            0.10,
            server_url=base_url,
            token="test",
        )

    err = exc_info.value
    assert err.status_code == 403
    assert err.is_model_disallowed is True
    assert err.is_budget_exceeded is False
    assert "not allowed" in str(err)


def test_meter_llm_usage_200_malformed_json(meter_server):
    base_url, handler = meter_server
    handler.status_code = 200
    handler.raw_response_bytes = b"not-valid-json"

    with pytest.raises(LLMMeterError) as exc_info:
        meter_llm_usage(
            "claude-sonnet-4-6",
            100,
            0.01,
            server_url=base_url,
            token="test",
        )

    err = exc_info.value
    assert err.status_code == 200
    assert str(err) == "Invalid meter response payload"
    assert err.response is None


def test_resolve_sandbox_token_from_file(tmp_path, monkeypatch):
    monkeypatch.delenv("DIVERGE_MACAROON_TOKEN", raising=False)
    token_file = tmp_path / "token"
    token_file.write_text("  sandbox-file-token-999 \n", encoding="utf-8")

    assert resolve_sandbox_token(token_file=str(token_file)) == "sandbox-file-token-999"

    monkeypatch.setenv("DIVERGE_TOKEN_FILE", str(token_file))
    assert resolve_sandbox_token() == "sandbox-file-token-999"


def test_meter_llm_usage_with_request_id_iteration_and_guidance(meter_server, tmp_path, monkeypatch):
    base_url, handler = meter_server
    monkeypatch.delenv("DIVERGE_MACAROON_TOKEN", raising=False)
    token_file = tmp_path / "token"
    token_file.write_text("minted-token-v1\n", encoding="utf-8")
    monkeypatch.setenv("DIVERGE_TOKEN_FILE", str(token_file))

    handler.response_body = {
        "allowed": True,
        "suspended": False,
        "totalTokensUsed": 3200,
        "totalCostUSD": 0.095,
        "iteration": 4,
        "maxIterations": 20,
        "guidance": "Switch to exponential backoff for retries",
    }

    resp = meter_llm_usage(
        "claude-sonnet-4-6",
        1200,
        0.035,
        request_id="task-1-iter-4",
        iteration=4,
        server_url=base_url,
    )

    assert resp.allowed is True
    assert resp.iteration == 4
    assert resp.max_iterations == 20
    assert resp.guidance == "Switch to exponential backoff for retries"
    assert len(handler.recorded_requests) == 1
    assert handler.recorded_requests[0]["auth"] == "Bearer minted-token-v1"
    assert handler.recorded_requests[0]["body"] == {
        "model": "claude-sonnet-4-6",
        "tokensUsed": 1200,
        "costUSD": 0.035,
        "requestId": "task-1-iter-4",
        "iteration": 4,
    }

    # Simulate kubelet rotating the mounted Secret token on resume
    token_file.write_text("minted-token-v2-resumed\n", encoding="utf-8")
    meter_llm_usage(
        "claude-sonnet-4-6",
        800,
        0.025,
        request_id="task-1-iter-5",
        iteration=5,
        server_url=base_url,
    )
    assert len(handler.recorded_requests) == 2
    assert handler.recorded_requests[1]["auth"] == "Bearer minted-token-v2-resumed"


def test_kafka_headers_and_context(monkeypatch):
    _current_env.set("")
    monkeypatch.delenv("DIVERGE_ENV", raising=False)
    monkeypatch.delenv("DIVERGE_HEADER_KEY", raising=False)

    token = set_environment("pr-77")
    try:
        headers = inject_kafka_headers([("x-trace", b"trace-1")])
    finally:
        reset_environment(token)

    assert ("x-trace", b"trace-1") in headers
    assert ("x-diverge-env", b"pr-77") in headers
    assert ("baggage", b"diverge-env=pr-77") in headers
    assert extract_kafka_context(headers) == "pr-77"

    with kafka_message_context(headers) as active_env:
        assert active_env == "pr-77"
        assert get_environment() == "pr-77"

    assert get_environment() == ""

    with kafka_message_context([("baggage", b"foo=bar, diverge-env=pr-88")]):
        assert get_environment() == "pr-88"


def test_resolve_kafka_topic(monkeypatch):
    _current_env.set("")
    monkeypatch.delenv("DIVERGE_ENV", raising=False)
    monkeypatch.delenv("DIVERGE_KAFKA_TOPIC_ORDERS_CREATED", raising=False)

    assert resolve_kafka_topic("orders.created") == "orders.created"

    token = set_environment("pr-77")
    try:
        assert resolve_kafka_topic("orders.created") == "orders.created.preview.pr-77"
    finally:
        reset_environment(token)

    assert (
        resolve_kafka_topic(
            "orders.created",
            environ={"DIVERGE_KAFKA_TOPIC_ORDERS_CREATED": "orders.created.custom"},
        )
        == "orders.created.custom"
    )


@dataclass
class _FakeTemporalPayload:
    data: bytes


def test_temporal_header_context(monkeypatch):
    _current_env.set("")
    monkeypatch.delenv("DIVERGE_ENV", raising=False)
    monkeypatch.delenv("DIVERGE_HEADER_KEY", raising=False)

    token = set_environment("sbx-temporal-1")
    try:
        hdrs = get_temporal_headers()
    finally:
        reset_environment(token)

    assert hdrs == {
        "x-diverge-env": "sbx-temporal-1",
        "baggage": "diverge-env=sbx-temporal-1",
    }

    with temporal_header_context({"x-diverge-env": _FakeTemporalPayload(data=b'"sbx-temporal-1"')}):
        assert get_environment() == "sbx-temporal-1"

    assert get_environment() == ""


def test_resolve_redis_prefix_and_key(monkeypatch):
    from diverge_sdk import resolve_redis_key, resolve_redis_prefix

    _current_env.set("")
    monkeypatch.delenv("DIVERGE_ENV", raising=False)
    monkeypatch.delenv("DIVERGE_REDIS_PREFIX", raising=False)
    monkeypatch.delenv("REDIS_KEY_PREFIX", raising=False)

    assert resolve_redis_prefix() == ""
    assert resolve_redis_key("session:user-123") == "session:user-123"

    token = set_environment("pr-42")
    try:
        assert resolve_redis_prefix() == "preview:pr-42:"
        prefixed = resolve_redis_key("session:user-123")
        assert prefixed == "preview:pr-42:session:user-123"
        assert resolve_redis_key(prefixed) == "preview:pr-42:session:user-123"
    finally:
        reset_environment(token)

    assert (
        resolve_redis_prefix(environ={"DIVERGE_REDIS_PREFIX": "preview:pr-42:cache"})
        == "preview:pr-42:cache:"
    )
    assert (
        resolve_redis_key(
            "user:1",
            environ={"DIVERGE_REDIS_PREFIX": "preview:pr-42:cache"},
        )
        == "preview:pr-42:cache:user:1"
    )
    assert (
        resolve_redis_key(
            "preview:pr-42:cache:user:1",
            environ={"DIVERGE_REDIS_PREFIX": "preview:pr-42:cache:"},
        )
        == "preview:pr-42:cache:user:1"
    )
    assert (
        resolve_redis_prefix(environ={"REDIS_KEY_PREFIX": "preview:pr-77:"})
        == "preview:pr-77:"
    )

