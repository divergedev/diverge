package argocd

import (
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestGenerateApplicationsSingleService(t *testing.T) {
	gen := &ApplicationGenerator{
		ArgoNamespace: "argocd",
		RepoURL:       "https://github.com/myorg/charts.git",
	}

	env := &v1alpha1.Environment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "diverge.io/v1alpha1",
			Kind:       "Environment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "preview-mr-42",
			UID:  "test-uid-123",
		},
	}

	services := []string{"api"}
	configs := map[string]ServiceConfig{
		"api": {Name: "api", Tag: "abc123", ChartPath: "charts/api"},
	}

	apps, err := gen.GenerateApplications(env, services, configs)
	require.NoError(t, err)
	require.Len(t, apps, 1)

	app := apps[0]
	assert.Equal(t, "argoproj.io/v1alpha1", app.GetAPIVersion())
	assert.Equal(t, "Application", app.GetKind())
	assert.Equal(t, "diverge-preview-mr-42-api", app.GetName())
	assert.Equal(t, "argocd", app.GetNamespace())

	// Labels
	labels := app.GetLabels()
	assert.Equal(t, "preview-mr-42", labels["diverge.io/environment"])
	assert.Equal(t, "api", labels["diverge.io/service"])
	assert.Equal(t, "diverge", labels["diverge.io/managed-by"])

	// Owner reference
	ownerRefs := app.GetOwnerReferences()
	require.Len(t, ownerRefs, 1)
	assert.Equal(t, "Environment", ownerRefs[0].Kind)
	assert.Equal(t, "preview-mr-42", ownerRefs[0].Name)

	// Source
	repoURL, _, _ := unstructured.NestedString(app.Object, "spec", "source", "repoURL")
	assert.Equal(t, "https://github.com/myorg/charts.git", repoURL)

	path, _, _ := unstructured.NestedString(app.Object, "spec", "source", "path")
	assert.Equal(t, "charts/api", path)

	// Helm params
	params, _, _ := unstructured.NestedSlice(app.Object, "spec", "source", "helm", "parameters")
	require.Len(t, params, 1)
	param := params[0].(map[string]interface{})
	assert.Equal(t, "image.tag", param["name"])
	assert.Equal(t, "abc123", param["value"])

	// Destination
	ns, _, _ := unstructured.NestedString(app.Object, "spec", "destination", "namespace")
	assert.Equal(t, "preview-mr-42", ns)

	// Sync policy
	autoSync, _, _ := unstructured.NestedMap(app.Object, "spec", "syncPolicy", "automated")
	assert.Equal(t, true, autoSync["prune"])
	assert.Equal(t, true, autoSync["selfHeal"])

	// CreateNamespace=true
	syncOpts, _, _ := unstructured.NestedSlice(app.Object, "spec", "syncPolicy", "syncOptions")
	require.Len(t, syncOpts, 1)
	assert.Equal(t, "CreateNamespace=true", syncOpts[0])
}

func TestGenerateApplicationsMultipleServices(t *testing.T) {
	gen := &ApplicationGenerator{
		ArgoNamespace: "argocd",
		RepoURL:       "https://github.com/myorg/charts.git",
	}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "preview-mr-42", UID: "uid"},
	}

	services := []string{"api", "web", "worker"}
	configs := map[string]ServiceConfig{
		"api":    {Name: "api", Tag: "v1.0.0", ChartPath: "charts/api"},
		"web":    {Name: "web", Tag: "v2.0.0", ChartPath: "charts/web"},
		"worker": {Name: "worker", Tag: "v3.0.0", ChartPath: "charts/worker"},
	}

	apps, err := gen.GenerateApplications(env, services, configs)
	require.NoError(t, err)
	require.Len(t, apps, 3)

	// Each app should have a unique name and the correct service label
	names := make(map[string]bool)
	for _, app := range apps {
		names[app.GetName()] = true
		assert.Equal(t, "preview-mr-42", app.GetLabels()["diverge.io/environment"])
	}
	assert.True(t, names["diverge-preview-mr-42-api"])
	assert.True(t, names["diverge-preview-mr-42-web"])
	assert.True(t, names["diverge-preview-mr-42-worker"])
}

func TestGenerateApplicationsDeltaDeployment(t *testing.T) {
	gen := &ApplicationGenerator{
		ArgoNamespace: "argocd",
		RepoURL:       "https://github.com/myorg/charts.git",
	}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "preview-mr-42", UID: "uid"},
	}

	// Only "api" changed — delta deployment should only produce 1 Application
	changedServices := []string{"api"}
	configs := map[string]ServiceConfig{
		"api":    {Name: "api", Tag: "v1.1.0", ChartPath: "charts/api"},
		"web":    {Name: "web", Tag: "v2.0.0", ChartPath: "charts/web"},
		"worker": {Name: "worker", Tag: "v3.0.0", ChartPath: "charts/worker"},
	}

	apps, err := gen.GenerateApplications(env, changedServices, configs)
	require.NoError(t, err)
	require.Len(t, apps, 1, "delta deployment should only create apps for changed services")
	assert.Equal(t, "diverge-preview-mr-42-api", apps[0].GetName())
}

func TestGenerateApplicationsEmptyServices(t *testing.T) {
	gen := &ApplicationGenerator{ArgoNamespace: "argocd", RepoURL: "https://example.com"}
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "preview-mr-42", UID: "uid"},
	}

	apps, err := gen.GenerateApplications(env, []string{}, map[string]ServiceConfig{})
	require.NoError(t, err)
	assert.Empty(t, apps)
}

func TestGenerateApplicationsMissingConfig(t *testing.T) {
	gen := &ApplicationGenerator{ArgoNamespace: "argocd", RepoURL: "https://example.com"}
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "preview-mr-42", UID: "uid"},
	}

	_, err := gen.GenerateApplications(env, []string{"missing"}, map[string]ServiceConfig{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "service config not found for missing")
}

func TestGenerateApplicationsOwnerReferenceCascade(t *testing.T) {
	gen := &ApplicationGenerator{ArgoNamespace: "argocd", RepoURL: "https://example.com"}
	env := &v1alpha1.Environment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "diverge.io/v1alpha1",
			Kind:       "Environment",
		},
		ObjectMeta: metav1.ObjectMeta{Name: "preview-mr-42", UID: "cascade-uid"},
	}

	configs := map[string]ServiceConfig{
		"api": {Name: "api", Tag: "v1", ChartPath: "charts/api"},
	}

	apps, err := gen.GenerateApplications(env, []string{"api"}, configs)
	require.NoError(t, err)

	ownerRefs := apps[0].GetOwnerReferences()
	require.Len(t, ownerRefs, 1)
	assert.Equal(t, "diverge.io/v1alpha1", ownerRefs[0].APIVersion)
	assert.Equal(t, "Environment", ownerRefs[0].Kind)
	assert.Equal(t, "preview-mr-42", ownerRefs[0].Name)
	assert.True(t, *ownerRefs[0].Controller, "should be controller reference")
	assert.True(t, *ownerRefs[0].BlockOwnerDeletion, "should block owner deletion")
}
