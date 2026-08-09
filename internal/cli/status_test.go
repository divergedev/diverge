package cli

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func TestStatusShowsEnvironmentDetails(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "preview-mr-42",
			Namespace: "default",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Source: divergeiov1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "divergedev/diverge",
				Branch:   "feat/preview",
				MR:       42,
			},
			Routing: divergeiov1alpha1.EnvironmentRouting{
				Mode:        "header",
				HeaderKey:   "x-diverge-env",
				HeaderValue: "preview-mr-42",
			},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			Phase:    "Ready",
			URL:      "https://preview-mr-42.example.com",
			Services: []string{"api", "web"},
			Conditions: []metav1.Condition{
				{
					Type:    "Ready",
					Status:  "True",
					Reason:  "AllServicesReady",
					Message: "All services deployed",
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(env).
		Build()

	app := &App{Namespace: "default", Client: fakeClient}
	cmd := newStatusCmd(app)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"preview-mr-42"})

	err := cmd.Execute()
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "preview-mr-42")
	assert.Contains(t, output, "Ready")
	assert.Contains(t, output, "https://preview-mr-42.example.com")
	assert.Contains(t, output, "api")
	assert.Contains(t, output, "web")
	assert.Contains(t, output, "header")
	assert.Contains(t, output, "x-diverge-env")
	assert.Contains(t, output, "AllServicesReady")
}

func TestStatusNotFound(t *testing.T) {
	fakeClient := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		Build()

	app := &App{Namespace: "default", Client: fakeClient}
	cmd := newStatusCmd(app)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"nonexistent"})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get environment")
}

func TestStatusRequiresArg(t *testing.T) {
	app := &App{}
	cmd := newStatusCmd(app)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "accepts 1 arg(s)")
}
