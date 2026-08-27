package sdk

import (
	"encoding/base64"
	"fmt"

	"google.golang.org/protobuf/proto"

	divergev1alpha1 "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
)

// BinaryHeaderKey is the HTTP header key used to propagate the binary context.
const BinaryHeaderKey = "x-diverge-context-bin"

// EncodePropagationContext marshals a PropagationContext to protobuf and encodes it as base64.
func EncodePropagationContext(ctx *divergev1alpha1.PropagationContext) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("cannot encode nil PropagationContext")
	}

	b, err := proto.Marshal(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to marshal PropagationContext: %w", err)
	}

	return base64.StdEncoding.EncodeToString(b), nil
}

// DecodePropagationContext decodes a base64 string and unmarshals it into a PropagationContext.
func DecodePropagationContext(encoded string) (*divergev1alpha1.PropagationContext, error) {
	b, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64 PropagationContext: %w", err)
	}

	var ctx divergev1alpha1.PropagationContext
	if err := proto.Unmarshal(b, &ctx); err != nil {
		return nil, fmt.Errorf("failed to unmarshal PropagationContext: %w", err)
	}

	return &ctx, nil
}
