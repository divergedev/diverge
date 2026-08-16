package temporal

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

type mockHeaderWriter struct {
	headers map[string][]byte
}

func (m *mockHeaderWriter) Set(key string, value []byte) {
	if m.headers == nil {
		m.headers = make(map[string][]byte)
	}
	m.headers[key] = value
}

type mockHeaderReader struct {
	headers map[string][]byte
}

func (m *mockHeaderReader) Get(key string) ([]byte, bool) {
	val, ok := m.headers[key]
	return val, ok
}

func (m *mockHeaderReader) ForEachKey(handler func(string, []byte) error) error {
	for k, v := range m.headers {
		if err := handler(k, v); err != nil {
			return err
		}
	}
	return nil
}

func TestContextPropagator_InjectExtract(t *testing.T) {
	prop := NewContextPropagator()
	ctx := WithEnv(context.Background(), "pr-123")

	writer := &mockHeaderWriter{}
	err := prop.Inject(ctx, writer)
	assert.NoError(t, err)
	assert.Equal(t, []byte("pr-123"), writer.headers[HeaderKey])

	reader := &mockHeaderReader{headers: writer.headers}
	extractedCtx, err := prop.Extract(context.Background(), reader)
	assert.NoError(t, err)
	assert.Equal(t, "pr-123", EnvFromContext(extractedCtx))
}

func TestContextPropagator_FallbackEnv(t *testing.T) {
	t.Setenv("DIVERGE_ENV", "env-fallback")

	prop := NewContextPropagator()
	ctx := context.Background()

	writer := &mockHeaderWriter{}
	err := prop.Inject(ctx, writer)
	assert.NoError(t, err)
	assert.Equal(t, []byte("env-fallback"), writer.headers[HeaderKey])
}
