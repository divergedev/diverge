import { describe, it, expect } from 'vitest';
import { encodePropagationContext, decodePropagationContext, RoutingMode, PropagationContext } from '../src/propagation.js';

describe('Propagation', () => {
  it('should encode and decode empty context', () => {
    const ctx: PropagationContext = {
      environment: "",
      routing_mode: RoutingMode.UNSPECIFIED,
      metadata: {}
    };
    const encoded = encodePropagationContext(ctx);
    expect(encoded).toBe("");

    const decoded = decodePropagationContext(encoded);
    expect(decoded.environment).toBe("");
    expect(decoded.routing_mode).toBe(RoutingMode.UNSPECIFIED);
    expect(decoded.metadata).toEqual({});
  });

  it('should encode and decode full context', () => {
    const ctx: PropagationContext = {
      environment: "test-env",
      routing_mode: RoutingMode.HEADER,
      metadata: { key1: "value1", key2: "value2" }
    };

    const encoded = encodePropagationContext(ctx);
    const decoded = decodePropagationContext(encoded);

    expect(decoded.environment).toBe("test-env");
    expect(decoded.routing_mode).toBe(RoutingMode.HEADER);
    expect(decoded.metadata).toEqual({ key1: "value1", key2: "value2" });
  });

  it('should be wire compatible', () => {
    // known-good value from Go SDK: environment="test-env", routing_mode=HEADER(1), metadata={}
    // Base64 without pad: Cgh0ZXN0LWVudhAB
    const encoded = "Cgh0ZXN0LWVudhAB";

    const decoded = decodePropagationContext(encoded);
    expect(decoded.environment).toBe("test-env");
    expect(decoded.routing_mode).toBe(RoutingMode.HEADER);
    expect(decoded.metadata).toEqual({});

    const ctx: PropagationContext = {
      environment: "test-env",
      routing_mode: RoutingMode.HEADER,
      metadata: {}
    };
    expect(encodePropagationContext(ctx)).toBe(encoded);
  });

  it('should throw on oversized payload', () => {
    const huge = "a".repeat(4097);
    expect(() => decodePropagationContext(huge)).toThrow("Encoded context too large");
  });
});
