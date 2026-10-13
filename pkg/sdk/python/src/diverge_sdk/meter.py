from dataclasses import dataclass
import json
import os
from typing import Any, Optional
import urllib.error
import urllib.request

DEFAULT_SERVER_URL = "http://diverge-server.diverge-system.svc:8080"
DEFAULT_TOKEN_FILE = "/etc/diverge/token/token"
METER_ENDPOINT_PATH = "/api/v1/agenttask/meter"


@dataclass
class LLMMeterResponse:
    allowed: bool
    suspended: bool
    total_tokens_used: int
    total_cost_usd: float
    reason: str = ""


class LLMMeterError(Exception):
    def __init__(
        self,
        message: str,
        status_code: int,
        response: Optional[LLMMeterResponse] = None,
    ) -> None:
        super().__init__(message)
        self.status_code = status_code
        self.response = response

    @property
    def is_budget_exceeded(self) -> bool:
        return self.status_code == 429

    @property
    def is_model_disallowed(self) -> bool:
        return self.status_code == 403


def resolve_sandbox_token(
    token: Optional[str] = None,
    token_file: Optional[str] = None,
) -> str:
    if token and token.strip():
        return token.strip()

    env_token = os.environ.get("DIVERGE_MACAROON_TOKEN")
    if env_token and env_token.strip():
        return env_token.strip()

    token_path = token_file or os.environ.get("DIVERGE_TOKEN_FILE") or DEFAULT_TOKEN_FILE
    try:
        with open(token_path, "r", encoding="utf-8") as f:
            file_token = f.read().strip()
            if file_token:
                return file_token
    except OSError as exc:
        raise ValueError(
            f"Failed to resolve Diverge sandbox token: no token provided via arguments or "
            f"DIVERGE_MACAROON_TOKEN, and could not read token file at {token_path}: {exc}"
        ) from exc

    raise ValueError(
        f"Failed to resolve Diverge sandbox token: token file at {token_path} is empty"
    )


def _parse_meter_response(data: Any) -> Optional[LLMMeterResponse]:
    if not isinstance(data, dict):
        return None
    return LLMMeterResponse(
        allowed=bool(data.get("allowed", False)),
        suspended=bool(data.get("suspended", False)),
        total_tokens_used=int(
            data.get("totalTokensUsed", data.get("total_tokens_used", 0))
        ),
        total_cost_usd=float(
            data.get("totalCostUSD", data.get("total_cost_usd", 0.0))
        ),
        reason=str(data.get("reason", "") or ""),
    )


def meter_llm_usage(
    model: str,
    tokens_used: int,
    cost_usd: float,
    *,
    server_url: Optional[str] = None,
    token: Optional[str] = None,
    token_file: Optional[str] = None,
    timeout: float = 10.0,
) -> LLMMeterResponse:
    resolved_token = resolve_sandbox_token(token=token, token_file=token_file)

    raw_url = (
        server_url
        or os.environ.get("DIVERGE_METER_URL")
        or os.environ.get("DIVERGE_SERVER_URL")
        or DEFAULT_SERVER_URL
    )
    normalized_url = raw_url.rstrip("/")
    if normalized_url.endswith(METER_ENDPOINT_PATH):
        endpoint = normalized_url
    else:
        endpoint = f"{normalized_url}{METER_ENDPOINT_PATH}"

    payload = json.dumps(
        {
            "model": model,
            "tokensUsed": tokens_used,
            "costUSD": cost_usd,
        }
    ).encode("utf-8")

    req = urllib.request.Request(
        endpoint,
        data=payload,
        headers={
            "Content-Type": "application/json",
            "Authorization": f"Bearer {resolved_token}",
        },
        method="POST",
    )

    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw_body = resp.read().decode("utf-8", errors="replace")
            try:
                parsed = json.loads(raw_body) if raw_body else {}
            except ValueError:
                parsed = None
            meter_resp = _parse_meter_response(parsed)
            if meter_resp is None:
                raise LLMMeterError(
                    "Invalid meter response payload",
                    resp.status,
                    None,
                )
            return meter_resp
    except urllib.error.HTTPError as exc:
        raw_err = exc.read().decode("utf-8", errors="replace") if exc.fp else ""
        parsed_err = None
        if raw_err:
            try:
                parsed_err = _parse_meter_response(json.loads(raw_err))
            except ValueError:
                parsed_err = None
        message = (
            parsed_err.reason
            if parsed_err and parsed_err.reason
            else f"Metering failed with status {exc.code}"
        )
        raise LLMMeterError(message, exc.code, parsed_err) from exc
