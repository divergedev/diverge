from .context import set_environment, get_header_key, reset_environment

class DivergASGIMiddleware:
    def __init__(self, app):
        self.app = app

    async def __call__(self, scope, receive, send):
        if scope["type"] not in ("http", "websocket"):
            return await self.app(scope, receive, send)

        header_key = get_header_key().lower()
        env_val = None

        for k, v in scope.get("headers", []):
            if k.lower() == header_key.encode('latin1'):
                env_val = v.decode('latin1')
                break

        token = None
        if env_val:
            token = set_environment(env_val)

        async def send_wrapper(message):
            if message["type"] == "http.response.start":
                from .context import get_environment
                curr_env = get_environment()
                if curr_env:
                    headers = list(message.get("headers", []))
                    h_key = get_header_key().lower().encode('latin1')
                    headers = [h for h in headers if h[0].lower() != h_key]
                    headers.append((h_key, curr_env.encode('latin1')))
                    message["headers"] = headers
            await send(message)

        try:
            await self.app(scope, receive, send_wrapper)
        finally:
            if token:
                reset_environment(token)


class _ClosingIterator:
    def __init__(self, iterable, token):
        self._iterable = iterable
        self._iterator = iter(iterable)
        self._token = token

    def __iter__(self):
        return self

    def __next__(self):
        return next(self._iterator)

    def close(self):
        try:
            if hasattr(self._iterable, "close"):
                self._iterable.close()
        finally:
            if self._token:
                reset_environment(self._token)


def diverge_wsgi_middleware(app):
    def middleware(environ, start_response):
        header_key = "HTTP_" + get_header_key().upper().replace("-", "_")
        env_val = environ.get(header_key)
        token = None
        if env_val:
            token = set_environment(env_val)

        def custom_start_response(status, response_headers, exc_info=None):
            from .context import get_environment
            curr_env = get_environment()
            if curr_env:
                h_key = get_header_key()
                response_headers = [(k, v) for k, v in response_headers if k.lower() != h_key.lower()]
                response_headers.append((h_key, curr_env))
            return start_response(status, response_headers, exc_info)

        response = app(environ, custom_start_response)
        if token:
            return _ClosingIterator(response, token)
        return response
    return middleware
