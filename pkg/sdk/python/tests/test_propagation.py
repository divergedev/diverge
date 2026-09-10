import pytest
from diverge_sdk.propagation import encode_propagation_context, decode_propagation_context, PropagationContext, RoutingMode

def test_encode_decode_roundtrip():
    ctx = PropagationContext(
        environment="test-env",
        routing_mode=RoutingMode.HEADER,
        metadata={"key1": "value1", "key2": "value2"}
    )
    encoded = encode_propagation_context(ctx)
    decoded = decode_propagation_context(encoded)

    assert decoded.environment == "test-env"
    assert decoded.routing_mode == RoutingMode.HEADER
    assert decoded.metadata == {"key1": "value1", "key2": "value2"}

def test_empty_context():
    ctx = PropagationContext()
    encoded = encode_propagation_context(ctx)
    assert encoded == ""
    decoded = decode_propagation_context(encoded)
    assert decoded.environment == ""
    assert decoded.routing_mode == RoutingMode.UNSPECIFIED
    assert decoded.metadata == {}

def test_wire_compatibility():
    # known-good value from the Go SDK: environment="test-env", routing_mode=HEADER(1), metadata={}
    # Proto bytes: 0x0A 0x08 "test-env" 0x10 0x01
    # Base64 without pad: Cgh0ZXN0LWVudhAB
    encoded = "Cgh0ZXN0LWVudhAB"
    decoded = decode_propagation_context(encoded)

    assert decoded.environment == "test-env"
    assert decoded.routing_mode == RoutingMode.HEADER
    assert decoded.metadata == {}

    ctx = PropagationContext(environment="test-env", routing_mode=RoutingMode.HEADER)
    assert encode_propagation_context(ctx) == encoded

def test_size_limit():
    with pytest.raises(ValueError, match="too large"):
        decode_propagation_context("a" * 4097)
