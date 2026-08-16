package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	divergev1alpha1connect "github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockEnvServer struct {
	divergev1alpha1connect.UnimplementedEnvironmentServiceHandler
}

func (m *mockEnvServer) ListEnvironments(ctx context.Context, req *connect.Request[pb.ListEnvironmentsRequest]) (*connect.Response[pb.ListEnvironmentsResponse], error) {
	env := pb.Environment{
		Name:      "test-env",
		Namespace: req.Msg.Namespace,
	}
	return connect.NewResponse(&pb.ListEnvironmentsResponse{
		Environments: []*pb.Environment{&env},
	}), nil
}

func (m *mockEnvServer) GetEnvironment(ctx context.Context, req *connect.Request[pb.GetEnvironmentRequest]) (*connect.Response[pb.GetEnvironmentResponse], error) {
	env := pb.Environment{
		Name:      req.Msg.Name,
		Namespace: req.Msg.Namespace,
	}
	return connect.NewResponse(&pb.GetEnvironmentResponse{
		Environment: &env,
	}), nil
}

func TestConnectClient(t *testing.T) {
	mux := http.NewServeMux()
	path, handler := divergev1alpha1connect.NewEnvironmentServiceHandler(&mockEnvServer{})
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewConnectClient(server.URL, "dummy-token")
	ctx := context.Background()

	envs, err := client.ListEnvironments(ctx, "default")
	require.NoError(t, err)
	require.Len(t, envs, 1)
	assert.Equal(t, "test-env", envs[0].Name)
	assert.Equal(t, "default", envs[0].Namespace)

	env, err := client.GetEnvironment(ctx, "default", "my-env")
	require.NoError(t, err)
	require.NotNil(t, env)
	assert.Equal(t, "my-env", env.Name)
	assert.Equal(t, "default", env.Namespace)
}
