import requests
from .context import get_environment, get_header_key, BINARY_HEADER_KEY

class DivergSession(requests.Session):
    def __init__(self, propagation_context=None):
        super().__init__()
        self.propagation_context = propagation_context

    def prepare_request(self, request):
        env = get_environment()
        if env:
            request.headers[get_header_key()] = env

        if self.propagation_context:
            from .propagation import encode_propagation_context
            request.headers[BINARY_HEADER_KEY] = encode_propagation_context(self.propagation_context)

        return super().prepare_request(request)
