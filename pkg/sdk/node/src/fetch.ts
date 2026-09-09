import { getEnvironment, getHeaderKey, BINARY_HEADER_KEY } from './context.js';
import { PropagationContext, encodePropagationContext } from './propagation.js';

export function divergeFetch(propagationContext?: PropagationContext) {
  return async function(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
    const env = getEnvironment();

    // Create new headers object
    const headers = new Headers(init?.headers);

    if (env) {
      headers.set(getHeaderKey(), env);
    }

    if (propagationContext) {
      headers.set(BINARY_HEADER_KEY, encodePropagationContext(propagationContext));
    }

    return fetch(input, {
      ...init,
      headers
    });
  };
}
