import pytest
from diverge_sdk.middleware import DivergASGIMiddleware, diverge_wsgi_middleware
from diverge_sdk.context import get_environment, _current_env

@pytest.mark.asyncio
async def test_asgi_middleware():
    _current_env.set("")

    async def app(scope, receive, send):
        assert get_environment() == "test-env"
        await send({"type": "http.response.start", "headers": []})

    middleware = DivergASGIMiddleware(app)

    scope = {
        "type": "http",
        "headers": [(b"x-diverge-env", b"test-env")]
    }

    responses = []
    async def send(msg):
        responses.append(msg)

    await middleware(scope, None, send)

    assert len(responses) == 1
    resp_headers = responses[0]["headers"]
    assert (b"x-diverge-env", b"test-env") in resp_headers

def test_wsgi_middleware():
    _current_env.set("")

    def app(environ, start_response):
        assert get_environment() == "test-env"
        start_response("200 OK", [])
        return [b"OK"]

    middleware = diverge_wsgi_middleware(app)

    environ = {
        "HTTP_X_DIVERGE_ENV": "test-env"
    }

    responses = []
    def start_response(status, headers, exc_info=None):
        responses.append(headers)

    result = middleware(environ, start_response)

    assert list(result) == [b"OK"]
    assert len(responses) == 1
    resp_headers = responses[0]
    assert ("x-diverge-env", "test-env") in resp_headers
