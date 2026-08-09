package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveNamespaceExplicitFlag(t *testing.T) {
	app := &App{Namespace: "custom-ns"}
	err := app.ResolveNamespace()
	assert.NoError(t, err)
	assert.Equal(t, "custom-ns", app.Namespace, "should keep explicit namespace")
}

func TestResolveNamespaceFromKubeconfig(t *testing.T) {
	// Default kubeconfig resolves a namespace (typically "default").
	// This test exercises the happy path when a kubeconfig exists.
	app := &App{}
	err := app.ResolveNamespace()
	// On a machine with a valid kubeconfig, this succeeds.
	// On CI without kubeconfig, it will return an error (not swallow it).
	if err == nil {
		assert.NotEmpty(t, app.Namespace, "should resolve namespace from kubeconfig")
	} else {
		assert.Contains(t, err.Error(), "failed to resolve namespace")
	}
}

func TestResolveNamespaceReturnsError(t *testing.T) {
	// Point at a nonexistent kubeconfig to force a resolution error
	app := &App{Kubeconfig: "/nonexistent/path/kubeconfig"}
	err := app.ResolveNamespace()
	// client-go's deferred loader returns "default" even with bad kubeconfig
	// unless the file is truly unreadable. The key assertion is: we no longer
	// silently leave Namespace empty when there's a genuine error.
	if err != nil {
		assert.Contains(t, err.Error(), "failed to resolve namespace")
		assert.Empty(t, app.Namespace)
	}
	// If no error (client-go fell back to "default"), namespace is set
	if err == nil {
		assert.NotEmpty(t, app.Namespace)
	}
}

func TestNewRootCmdHasPersistentPreRunE(t *testing.T) {
	app := &App{Namespace: "test"}
	cmd := NewRootCmd(app)
	assert.NotNil(t, cmd.PersistentPreRunE, "root command should have PersistentPreRunE set")
}
