package server

import (
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestNewServeMux_WithOTel(t *testing.T) {
	cfg := ServeMuxConfig{
		Client: fake.NewClientBuilder().Build(),
	}

	mux, tunnelMgr := NewServeMux(cfg)
	require.NotNil(t, mux)
	require.NotNil(t, tunnelMgr)
}
