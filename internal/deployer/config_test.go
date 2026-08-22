package deployer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDotDivergeConfig(t *testing.T) {
	// editorconfig-checker-disable
	tests := []struct {
		name     string
		input    string
		wantSvc  string
		wantPort int32
		wantErr  bool
	}{
		{
			name: "valid config",
			input: `
apiVersion: diverge.io/v1alpha1
kind: ServicePreview
metadata:
  name: payments-api
spec:
  namespace: demo-bank
  serviceName: payments-api
  port: 8080
  routing:
    headerKey: x-preview-id
  container:
    env:
      - name: LOG_LEVEL
        value: debug
`,
			wantSvc:  "payments-api",
			wantPort: 8080,
		},
		{
			name: "default port",
			input: `
apiVersion: diverge.io/v1alpha1
kind: ServicePreview
metadata:
  name: test-svc
spec:
  serviceName: test-svc
`,
			wantSvc:  "test-svc",
			wantPort: 8080,
		},
		{
			name: "missing serviceName",
			input: `
apiVersion: diverge.io/v1alpha1
kind: ServicePreview
metadata:
  name: test
spec:
  port: 8080
`,
			wantErr: true,
		},
		{
			name:    "invalid yaml",
			input:   `{{{invalid`,
			wantErr: true,
		},
		{
			name: "invalid port -1",
			input: `
apiVersion: diverge.io/v1alpha1
kind: ServicePreview
metadata:
  name: test
spec:
  serviceName: test
  port: -1
`,
			wantErr: true,
		},
		{
			name: "invalid port 65536",
			input: `
apiVersion: diverge.io/v1alpha1
kind: ServicePreview
metadata:
  name: test
spec:
  serviceName: test
  port: 65536
`,
			wantErr: true,
		},
	}
	// editorconfig-checker-enable

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ParseDotDivergeConfig([]byte(tt.input))
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantSvc, cfg.Spec.ServiceName)
			assert.Equal(t, tt.wantPort, cfg.Spec.Port)
		})
	}
}

func TestToServicePreviewConfig(t *testing.T) {
	// editorconfig-checker-disable
	cfg, err := ParseDotDivergeConfig([]byte(`
apiVersion: diverge.io/v1alpha1
kind: ServicePreview
metadata:
  name: payments-api
spec:
  namespace: demo-bank
  serviceName: payments-api
  port: 9090
  routing:
    parentRef: banking-waypoint
    headerKey: x-preview-id
  container:
    env:
      - name: DB_URL
        value: postgres://localhost/preview
`))
	// editorconfig-checker-enable
	require.NoError(t, err)

	spc := cfg.ToServicePreviewConfig("registry/payments-api:mr-42")

	assert.Equal(t, "payments-api", spc.ServiceName)
	assert.Equal(t, int32(9090), spc.Port)
	assert.Equal(t, "registry/payments-api:mr-42", spc.Image)
	assert.Equal(t, "banking-waypoint", spc.ParentRef)
	require.Len(t, spc.Env, 1)
	assert.Equal(t, "DB_URL", spc.Env[0].Name)
	assert.Equal(t, "postgres://localhost/preview", spc.Env[0].Value)
}

func TestToServicePreviewConfig_WebSocket(t *testing.T) {
	// editorconfig-checker-disable
	cfg, err := ParseDotDivergeConfig([]byte(`
apiVersion: diverge.io/v1alpha1
kind: ServicePreview
metadata:
  name: payments-api
spec:
  namespace: demo-bank
  serviceName: payments-api
  port: 9090
  websocket:
    enabled: true
    path: /ws
    timeout: 3600s
`))
	// editorconfig-checker-enable
	require.NoError(t, err)

	spc := cfg.ToServicePreviewConfig("registry/payments-api:mr-42")

	require.NotNil(t, spc.WebSocket)
	assert.True(t, spc.WebSocket.Enabled)
	assert.Equal(t, "/ws", spc.WebSocket.Path)
	assert.Equal(t, "3600s", spc.WebSocket.Timeout)
}
