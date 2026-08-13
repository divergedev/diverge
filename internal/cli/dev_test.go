package cli

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func TestDevCmd_InterceptAndRelease(t *testing.T) {
	s := runtime.NewScheme()
	_ = divergeiov1alpha1.AddToScheme(s)

	c := fake.NewClientBuilder().WithScheme(s).Build()
	app := &App{Client: c}

	// Create a PreviewGroup first
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "dev-test"},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "my-service", Mode: divergeiov1alpha1.ServiceModeImage},
			},
		},
	}
	require.NoError(t, c.Create(context.Background(), pg))

	// Intercept test
	cmd := newPreviewInterceptCmd(app)
	cmd.SetArgs([]string{"my-service", "--group", "dev-test", "--endpoint", "10.0.0.1:8080"})
	require.NoError(t, cmd.Execute())

	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "dev-test"}, pg))
	assert.Equal(t, divergeiov1alpha1.ServiceModeLocal, pg.Spec.Services[0].Mode)
	assert.Equal(t, "10.0.0.1:8080", pg.Spec.Services[0].Endpoint)

	// Release test
	releaseCmd := newPreviewReleaseCmd(app)
	releaseCmd.SetArgs([]string{"my-service", "--group", "dev-test"})
	require.NoError(t, releaseCmd.Execute())

	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "dev-test"}, pg))
	assert.Equal(t, divergeiov1alpha1.ServiceModeImage, pg.Spec.Services[0].Mode)
	assert.Empty(t, pg.Spec.Services[0].Endpoint)
}

func TestDevCmd_Intercept_MissingGroup(t *testing.T) {
	s := runtime.NewScheme()
	_ = divergeiov1alpha1.AddToScheme(s)

	c := fake.NewClientBuilder().WithScheme(s).Build()
	app := &App{Client: c}

	cmd := newPreviewInterceptCmd(app)
	cmd.SetArgs([]string{"my-service", "--group", "nonexistent", "--endpoint", "10.0.0.1:8080"})
	err := cmd.Execute()
	assert.Error(t, err, "intercept should fail for nonexistent group")
}

func TestDevCmd_Release_MissingGroup(t *testing.T) {
	s := runtime.NewScheme()
	_ = divergeiov1alpha1.AddToScheme(s)

	c := fake.NewClientBuilder().WithScheme(s).Build()
	app := &App{Client: c}

	cmd := newPreviewReleaseCmd(app)
	cmd.SetArgs([]string{"my-service", "--group", "nonexistent"})
	err := cmd.Execute()
	assert.Error(t, err, "release should fail for nonexistent group")
}
