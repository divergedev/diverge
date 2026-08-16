package temporal

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"

	"github.com/divergedev/diverge/pkg/sdk"
)

type mockHeader struct {
	payloads map[string]*commonpb.Payload
}

func (m *mockHeader) Set(key string, value *commonpb.Payload) {
	if m.payloads == nil {
		m.payloads = make(map[string]*commonpb.Payload)
	}
	m.payloads[key] = value
}

func (m *mockHeader) Get(key string) (*commonpb.Payload, bool) {
	if p, ok := m.payloads[key]; ok {
		return p, true
	}
	return nil, false
}

func (m *mockHeader) ForEachKey(handler func(string, *commonpb.Payload) error) error {
	for k, v := range m.payloads {
		if err := handler(k, v); err != nil {
			return err
		}
	}
	return nil
}

func TestPropagator_InjectExtract(t *testing.T) {
	p := NewPropagator()

	// Inject
	ctx := sdk.WithEnvironment(context.Background(), "my-env")
	header := &mockHeader{}
	err := p.Inject(ctx, header)
	require.NoError(t, err)

	assert.NotNil(t, header.payloads[sdk.GetHeaderKey()])

	// Extract
	ctx2, err := p.Extract(context.Background(), header)
	require.NoError(t, err)

	assert.Equal(t, "my-env", sdk.EnvironmentFromContext(ctx2))
}

func TestPropagator_OverwriteSemantics(t *testing.T) {
	p := NewPropagator()
	p.EnvName = "preview-env"

	// Inject should ignore ctx and use p.EnvName
	ctx := sdk.WithEnvironment(context.Background(), "untrusted-env")
	header := &mockHeader{}
	err := p.Inject(ctx, header)
	require.NoError(t, err)

	var injectedEnv string
	err = converter.GetDefaultDataConverter().FromPayload(header.payloads[sdk.GetHeaderKey()], &injectedEnv)
	require.NoError(t, err)
	assert.Equal(t, "preview-env", injectedEnv)

	// Extract should ignore header and use p.EnvName
	header2 := &mockHeader{}
	payload, _ := converter.GetDefaultDataConverter().ToPayload("forged-env")
	header2.Set(sdk.GetHeaderKey(), payload)

	ctx2, err := p.Extract(context.Background(), header2)
	require.NoError(t, err)
	assert.Equal(t, "preview-env", sdk.EnvironmentFromContext(ctx2))
}
