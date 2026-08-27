package sdk_test

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"pgregory.net/rapid"

	divergev1alpha1 "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/pkg/sdk"
)

func TestEncodeDecodeRoundtrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		ctx := &divergev1alpha1.PropagationContext{
			Environment: rapid.String().Draw(t, "environment"),
			RoutingMode: divergev1alpha1.RoutingMode(rapid.Int32Range(0, 3).Draw(t, "routing_mode")),
			Metadata:    rapid.MapOf(rapid.String(), rapid.String()).Draw(t, "metadata"),
		}

		encoded, err := sdk.EncodePropagationContext(ctx)
		require.NoError(t, err)

		decoded, err := sdk.DecodePropagationContext(encoded)
		require.NoError(t, err)

		require.True(t, proto.Equal(ctx, decoded))
	})
}

func TestDecodePropagationContext_InvalidBase64(t *testing.T) {
	_, err := sdk.DecodePropagationContext("invalid-base64!")
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to decode base64")
}

func TestDecodePropagationContext_InvalidProto(t *testing.T) {
	invalidProto := []byte("invalid proto bytes")
	encoded := base64.StdEncoding.EncodeToString(invalidProto)

	_, err := sdk.DecodePropagationContext(encoded)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to unmarshal")
}

func TestEncodePropagationContext_Nil(t *testing.T) {
	_, err := sdk.EncodePropagationContext(nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot encode nil")
}
