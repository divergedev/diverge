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

func TestOpenCmdRejectsNonHTTPScheme(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{"file scheme", "file:///etc/passwd", "refusing to open URL with scheme"},
		{"javascript scheme", "javascript:alert(1)", "refusing to open URL with scheme"},
		{"ftp scheme", "ftp://evil.com", "refusing to open URL with scheme"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := &divergeiov1alpha1.Environment{
				ObjectMeta: metav1.ObjectMeta{Name: "pr-42", Namespace: "default"},
				Status:     divergeiov1alpha1.EnvironmentStatus{URL: tt.url},
			}
			_, err := buildPreviewURL(env)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestOpenCmdRejectsEmptyHostname(t *testing.T) {
	app := &App{Namespace: "default"}
	cmd := newOpenCmd(app)
	assert.NotNil(t, cmd)
	assert.Equal(t, "open <name>", cmd.Use)

	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "pr-42", Namespace: "default"},
		Status:     divergeiov1alpha1.EnvironmentStatus{URL: "https:///path"},
	}
	_, err := buildPreviewURL(env)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has no hostname")
}

func TestBuildPreviewURLAndMagicFlag(t *testing.T) {
	origOpenBrowser := openBrowser
	defer func() { openBrowser = origOpenBrowser }()

	var openedURL string
	openBrowser = func(targetURL string) error {
		openedURL = targetURL
		return nil
	}

	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-42",
			Namespace: "default",
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Routing: divergeiov1alpha1.EnvironmentRouting{
				Mode:        "header",
				HeaderKey:   "x-diverge-env",
				HeaderValue: "pr-42",
			},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			Phase: divergeiov1alpha1.PhaseRunning,
			URL:   "https://app.example.com",
		},
	}

	// Default buildPreviewURL preserves subdomain preview URL
	defaultURL, err := buildPreviewURL(env)
	require.NoError(t, err)
	assert.Equal(t, "https://pr-42.preview.app.example.com", defaultURL)

	// Magic buildPreviewURL constructs cookie-setting URL
	magicURL, err := buildPreviewURL(env, true)
	require.NoError(t, err)
	assert.Equal(t, "https://app.example.com?_diverge_env=pr-42", magicURL)

	// Execute diverge open --magic pr-42
	fakeClient := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(env).
		Build()
	app := &App{Namespace: "default", Client: fakeClient}
	cmd := newOpenCmd(app)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--magic", "pr-42"})

	require.NoError(t, cmd.Execute())
	assert.Equal(t, "https://app.example.com?_diverge_env=pr-42", openedURL)
	assert.Contains(t, out.String(), "Opening https://app.example.com?_diverge_env=pr-42")
}

func TestOpenCmdPreviewGroupAndNoBrowser(t *testing.T) {
	origOpenBrowser := openBrowser
	defer func() { openBrowser = origOpenBrowser }()

	var browserCalled bool
	var openedURL string
	openBrowser = func(targetURL string) error {
		browserCalled = true
		openedURL = targetURL
		return nil
	}

	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-101",
			Namespace: "default",
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Routing: divergeiov1alpha1.PreviewGroupRouting{
				Mode:        "header",
				HeaderKey:   "x-diverge-env",
				HeaderValue: "pr-101",
			},
		},
		Status: divergeiov1alpha1.PreviewGroupStatus{
			Phase: divergeiov1alpha1.PreviewGroupPhaseRunning,
			Services: []divergeiov1alpha1.PreviewGroupServiceStatus{
				{
					Name: "checkout",
					URL:  "https://shop.example.com",
				},
			},
		},
	}

	// Unit test buildPreviewGroupURL directly
	pgDefaultURL, err := buildPreviewGroupURL(pg)
	require.NoError(t, err)
	assert.Equal(t, "https://pr-101.preview.shop.example.com", pgDefaultURL)

	pgMagicURL, err := buildPreviewGroupURL(pg, true)
	require.NoError(t, err)
	assert.Equal(t, "https://shop.example.com?_diverge_env=pr-101", pgMagicURL)

	fakeClient := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(pg).
		Build()

	t.Run("opens PreviewGroup with --no-browser (-p) without calling openBrowser", func(t *testing.T) {
		browserCalled = false
		app := &App{Namespace: "default", Client: fakeClient}
		cmd := newOpenCmd(app)
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs([]string{"-p", "pr-101"})

		require.NoError(t, cmd.Execute())
		assert.False(t, browserCalled)
		assert.Equal(t, "https://pr-101.preview.shop.example.com\n", out.String())
	})

	t.Run("opens PreviewGroup with --magic and launches browser", func(t *testing.T) {
		browserCalled = false
		openedURL = ""
		app := &App{Namespace: "default", Client: fakeClient}
		cmd := newOpenCmd(app)
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs([]string{"--magic", "pr-101"})

		require.NoError(t, cmd.Execute())
		assert.True(t, browserCalled)
		assert.Equal(t, "https://shop.example.com?_diverge_env=pr-101", openedURL)
		assert.Contains(t, out.String(), "Note: PreviewGroup uses header routing")
		assert.Contains(t, out.String(), "Opening https://shop.example.com?_diverge_env=pr-101")
	})
}
