from .context import get_environment, set_environment, get_header_key
from .propagation import encode_propagation_context, decode_propagation_context, PropagationContext, RoutingMode
from .middleware import DivergASGIMiddleware, diverge_wsgi_middleware

__all__ = [
    "get_environment",
    "set_environment",
    "get_header_key",
    "encode_propagation_context",
    "decode_propagation_context",
    "PropagationContext",
    "RoutingMode",
    "DivergASGIMiddleware",
    "diverge_wsgi_middleware",
]

try:
    from .requests_transport import DivergeSession
    __all__.append("DivergeSession")
except ImportError:
    # requests is an optional dependency
    pass
