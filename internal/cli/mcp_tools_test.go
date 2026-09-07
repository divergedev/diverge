package cli

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/protocgen/proto2mcp/pkg/mcpruntime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergev1alpha1 "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/pkg/doctor"
)

type mockEnvClient struct {
	getEnvResponse *divergev1alpha1.GetEnvironmentResponse
	getEnvErr      error

	streamLogsErr error
}

func (m *mockEnvClient) CreateEnvironment(ctx context.Context, req *connect.Request[divergev1alpha1.CreateEnvironmentRequest]) (*connect.Response[divergev1alpha1.CreateEnvironmentResponse], error) {
	return nil, nil
}
func (m *mockEnvClient) GetEnvironment(ctx context.Context, req *connect.Request[divergev1alpha1.GetEnvironmentRequest]) (*connect.Response[divergev1alpha1.GetEnvironmentResponse], error) {
	if m.getEnvErr != nil {
		return nil, m.getEnvErr
	}
	return connect.NewResponse(m.getEnvResponse), nil
}
func (m *mockEnvClient) ListEnvironments(ctx context.Context, req *connect.Request[divergev1alpha1.ListEnvironmentsRequest]) (*connect.Response[divergev1alpha1.ListEnvironmentsResponse], error) {
	return nil, nil
}
func (m *mockEnvClient) UpdateEnvironment(ctx context.Context, req *connect.Request[divergev1alpha1.UpdateEnvironmentRequest]) (*connect.Response[divergev1alpha1.UpdateEnvironmentResponse], error) {
	return nil, nil
}
func (m *mockEnvClient) DeleteEnvironment(ctx context.Context, req *connect.Request[divergev1alpha1.DeleteEnvironmentRequest]) (*connect.Response[divergev1alpha1.DeleteEnvironmentResponse], error) {
	return nil, nil
}
func (m *mockEnvClient) ExtendTTL(ctx context.Context, req *connect.Request[divergev1alpha1.ExtendTTLRequest]) (*connect.Response[divergev1alpha1.ExtendTTLResponse], error) {
	return nil, nil
}
func (m *mockEnvClient) WatchEnvironments(ctx context.Context, req *connect.Request[divergev1alpha1.WatchEnvironmentsRequest]) (*connect.ServerStreamForClient[divergev1alpha1.WatchEnvironmentsResponse], error) {
	return nil, nil
}
func (m *mockEnvClient) StreamLogs(ctx context.Context, req *connect.Request[divergev1alpha1.StreamLogsRequest]) (*connect.ServerStreamForClient[divergev1alpha1.StreamLogsResponse], error) {
	return nil, m.streamLogsErr // For simplicity, we just won't implement the full stream mock in this stub unless we need to. I'll mock it if needed.
}
func (m *mockEnvClient) ListHookJobs(ctx context.Context, req *connect.Request[divergev1alpha1.ListHookJobsRequest]) (*connect.Response[divergev1alpha1.ListHookJobsResponse], error) {
	return nil, nil
}
func (m *mockEnvClient) RetryHook(ctx context.Context, req *connect.Request[divergev1alpha1.RetryHookRequest]) (*connect.Response[divergev1alpha1.RetryHookResponse], error) {
	return nil, nil
}

func TestWaitForReady(t *testing.T) {
	registry := mcpruntime.NewToolRegistry()

	client := &mockEnvClient{
		getEnvResponse: &divergev1alpha1.GetEnvironmentResponse{
			Environment: &divergev1alpha1.Environment{
				Name:      "test-env",
				Namespace: "default",
				Status: &divergev1alpha1.EnvironmentStatus{
					Phase: "Ready",
				},
			},
		},
	}

	registerWaitForReady(registry, client)

	handler, ok := registry.Lookup("diverge_wait_for_ready")
	require.True(t, ok)

	args := []byte(`{"name": "test-env", "namespace": "default"}`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	res, err := handler(ctx, mcpruntime.ToolRequest{
		ToolName:  "diverge_wait_for_ready",
		Arguments: args,
	})
	require.NoError(t, err)
	assert.False(t, res.IsError)

	var data map[string]interface{}
	err = json.Unmarshal(res.Content, &data)
	require.NoError(t, err)
	assert.Equal(t, "Ready", data["phase"])
}

func TestContainsErrorLevel(t *testing.T) {
	assert.True(t, containsErrorLevel("this is an error line"))
	assert.True(t, containsErrorLevel("FATAL failure"))
	assert.True(t, containsErrorLevel("panic: runtime error"))
	assert.False(t, containsErrorLevel("info: starting up"))
}

func TestRegisterLoadtest(t *testing.T) {
	registry := mcpruntime.NewToolRegistry()
	registerLoadtest(registry)

	handler, ok := registry.Lookup("diverge_loadtest")
	require.True(t, ok)

	args := []byte(`{"target_url": "http://127.0.0.1:0", "duration_seconds": 1, "concurrency": 1}`)
	res, err := handler(context.Background(), mcpruntime.ToolRequest{
		ToolName:  "diverge_loadtest",
		Arguments: args,
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.NotEmpty(t, res.Content)
}

func TestRegisterDoctor(t *testing.T) {
	registry := mcpruntime.NewToolRegistry()
	client := &mockEnvClient{
		getEnvResponse: &divergev1alpha1.GetEnvironmentResponse{
			Environment: &divergev1alpha1.Environment{
				Name:      "test-env",
				Namespace: "default",
				Status: &divergev1alpha1.EnvironmentStatus{
					Phase: "Ready",
				},
			},
		},
	}
	registerDoctor(registry, client)

	handler, ok := registry.Lookup("diverge_doctor")
	require.True(t, ok)

	args := []byte(`{"name": "test-env", "namespace": "default"}`)
	res, err := handler(context.Background(), mcpruntime.ToolRequest{
		ToolName:  "diverge_doctor",
		Arguments: args,
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.False(t, res.IsError)
}

func TestRegisterLoadtest_InvalidJSON(t *testing.T) {
	registry := mcpruntime.NewToolRegistry()
	registerLoadtest(registry)

	handler, ok := registry.Lookup("diverge_loadtest")
	require.True(t, ok)

	_, err := handler(context.Background(), mcpruntime.ToolRequest{
		ToolName:  "diverge_loadtest",
		Arguments: []byte(`invalid json`),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid arguments")
}

func TestRegisterLoadtest_InvalidURL(t *testing.T) {
	registry := mcpruntime.NewToolRegistry()
	registerLoadtest(registry)

	handler, ok := registry.Lookup("diverge_loadtest")
	require.True(t, ok)

	res, err := handler(context.Background(), mcpruntime.ToolRequest{
		ToolName:  "diverge_loadtest",
		Arguments: []byte(`{"target_url": "ftp://unsupported.local"}`),
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.IsError)
	assert.Contains(t, string(res.Content), "must be a valid http or https URL")
}

func TestRegisterDoctor_Error(t *testing.T) {
	registry := mcpruntime.NewToolRegistry()
	client := &mockEnvClient{
		getEnvErr: assert.AnError,
	}
	registerDoctor(registry, client)

	handler, ok := registry.Lookup("diverge_doctor")
	require.True(t, ok)

	args := []byte(`{"name": "nonexistent", "namespace": "default"}`)
	res, err := handler(context.Background(), mcpruntime.ToolRequest{
		ToolName:  "diverge_doctor",
		Arguments: args,
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.IsError)
}

func TestRegisterDoctor_InvalidJSON(t *testing.T) {
	registry := mcpruntime.NewToolRegistry()
	client := &mockEnvClient{}
	registerDoctor(registry, client)

	handler, ok := registry.Lookup("diverge_doctor")
	require.True(t, ok)

	_, err := handler(context.Background(), mcpruntime.ToolRequest{
		ToolName:  "diverge_doctor",
		Arguments: []byte(`bad-json`),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid arguments")
}

func TestValidateTargetURL(t *testing.T) {
	// Valid URLs
	assert.NoError(t, validateTargetURL("http://localhost:8080/preview"))
	assert.NoError(t, validateTargetURL("https://preview.diverge.run/app"))
	assert.NoError(t, validateTargetURL("http://127.0.0.1:3000"))

	// Invalid schemes
	assert.Error(t, validateTargetURL("ftp://preview.example.com"))
	assert.Error(t, validateTargetURL("gopher://preview.example.com"))
	assert.Error(t, validateTargetURL("file:///etc/passwd"))

	// Prohibited destinations (cloud metadata & link-local)
	assert.Error(t, validateTargetURL("http://169.254.169.254/latest/meta-data/"))
	assert.Error(t, validateTargetURL("http://metadata.google.internal/computeMetadata/v1/"))
	assert.Error(t, validateTargetURL("http://metadata/computeMetadata/v1/"))
	assert.Error(t, validateTargetURL("http://169.254.10.20/service"))

	// Allowlist checking
	t.Setenv("DIVERGE_ALLOWED_HOSTS", "diverge.run,example.com")
	assert.NoError(t, validateTargetURL("https://preview.diverge.run/test"))
	assert.NoError(t, validateTargetURL("http://example.com/test"))
	assert.Error(t, validateTargetURL("https://unauthorized-domain.org/test"))
}

func TestRegisterLoadtest_SSRFProtection(t *testing.T) {
	registry := mcpruntime.NewToolRegistry()
	registerLoadtest(registry)

	handler, ok := registry.Lookup("diverge_loadtest")
	require.True(t, ok)

	// Blocked metadata IP
	args := []byte(`{"target_url": "http://169.254.169.254/latest/meta-data"}`)
	res, err := handler(context.Background(), mcpruntime.ToolRequest{
		ToolName:  "diverge_loadtest",
		Arguments: args,
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.IsError)
	assert.Contains(t, string(res.Content), "prohibited")
}

func TestRegisterDoctor_WithDiagnoser(t *testing.T) {
	scheme := k8sruntime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, divergeiov1alpha1.AddToScheme(scheme))

	podCrash := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-crash",
			Namespace: "test-ns",
			Labels: map[string]string{
				"diverge.io/environment": "test-env",
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "api",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{
							Reason:  "CrashLoopBackOff",
							Message: "Back-off 20s restarting failed container",
						},
					},
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(podCrash).Build()
	diagnoser := doctor.NewDiagnoser(fakeClient)

	registry := mcpruntime.NewToolRegistry()
	mockClient := &mockEnvClient{}
	registerDoctor(registry, mockClient, diagnoser)

	handler, ok := registry.Lookup("diverge_doctor")
	require.True(t, ok)

	args := []byte(`{"name": "test-env", "namespace": "test-ns"}`)
	res, err := handler(context.Background(), mcpruntime.ToolRequest{
		ToolName:  "diverge_doctor",
		Arguments: args,
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.IsError)

	var data map[string]interface{}
	err = json.Unmarshal(res.Content, &data)
	require.NoError(t, err)
	assert.False(t, data["healthy"].(bool))
	issues := data["issues"].([]interface{})
	require.NotEmpty(t, issues)
	assert.Contains(t, issues[0].(string), "CrashLoopBackOff")
}
