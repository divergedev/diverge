from .context import set_environment, get_header_key

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

        if env_val:
            set_environment(env_val)

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

        await self.app(scope, receive, send_wrapper)


def diverge_wsgi_middleware(app):
    def middleware(environ, start_response):
        header_key = "HTTP_" + get_header_key().upper().replace("-", "_")
        env_val = environ.get(header_key)
        if env_val:
            set_environment(env_val)

        def custom_start_response(status, response_headers, exc_info=None):
            from .context import get_environment
            curr_env = get_environment()
            if curr_env:
                h_key = get_header_key()
                response_headers = [(k, v) for k, v in response_headers if k.lower() != h_key.lower()]
                response_headers.append((h_key, curr_env))
            return start_response(status, response_headers, exc_info)

        return app(environ, custom_start_response)
    return middleware
