package argocd

import (
	"context"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGenerateApplicationSet(t *testing.T) {
	gen := &ApplicationSetGenerator{
		ArgoNamespace: "argocd",
		RepoURL:       "https://charts.example.com",
	}

	env := &v1alpha1.Environment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "diverge.io/v1alpha1",
			Kind:       "Environment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "pr-123",
			UID:  "test-uid-123",
		},
	}

	services := []string{"svc1"}
	configs := map[string]ServiceConfig{
		"svc1": {Name: "svc1", Tag: "latest", ChartPath: "charts/svc1"},
	}

	appSet, err := gen.GenerateApplicationSet(context.Background(), env, services, configs)
	assert.NoError(t, err)
	assert.NotNil(t, appSet)

	assert.Equal(t, "argoproj.io/v1alpha1", appSet.GetAPIVersion())
	assert.Equal(t, "ApplicationSet", appSet.GetKind())
	assert.Equal(t, "diverge-pr-123", appSet.GetName())
	assert.Equal(t, "argocd", appSet.GetNamespace())

	labels := appSet.GetLabels()
	assert.Equal(t, "pr-123", labels["diverge.io/environment"])
	assert.Equal(t, "diverge", labels["diverge.io/managed-by"])

	spec, found, err := unstructuredNestedMap(appSet.Object, "spec")
	assert.NoError(t, err)
	assert.True(t, found)

	generators, found, err := unstructuredNestedSlice(spec, "generators")
	assert.NoError(t, err)
	assert.True(t, found)
	assert.Len(t, generators, 1)

	syncPolicy, found, err := unstructuredNestedMap(spec, "template", "spec", "syncPolicy")
	assert.NoError(t, err)
	assert.True(t, found)

	automated, found, err := unstructuredNestedMap(syncPolicy, "automated")
	assert.NoError(t, err)
	assert.True(t, found)

	assert.Equal(t, true, automated["prune"])
	assert.Equal(t, true, automated["selfHeal"])
}

func TestGenerateApplicationSetServices(t *testing.T) {
	gen := &ApplicationSetGenerator{
		ArgoNamespace: "argocd",
		RepoURL:       "https://charts.example.com",
	}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pr-123",
		},
	}

	services := []string{"svc1", "svc2"}
	configs := map[string]ServiceConfig{
		"svc1": {Name: "svc1", Tag: "v1.0.0", ChartPath: "charts/svc1"},
		"svc2": {Name: "svc2", Tag: "v2.0.0", ChartPath: "charts/svc2"},
	}

	appSet, err := gen.GenerateApplicationSet(context.Background(), env, services, configs)
	assert.NoError(t, err)
	assert.NotNil(t, appSet)

	spec, _, _ := unstructuredNestedMap(appSet.Object, "spec")
	generators, _, _ := unstructuredNestedSlice(spec, "generators")
	gen0, ok := generators[0].(map[string]interface{})
	assert.True(t, ok)
	list, ok := gen0["list"].(map[string]interface{})
	assert.True(t, ok)
	elements, ok := list["elements"].([]map[string]interface{}) // it is actually []interface{} but we built it as []map[string]interface{}
	// Actually unstructured converts arrays into []interface{} usually, but here we passed []map[string]interface{} directly. Let's assert based on that.

	assert.True(t, ok)
	assert.Len(t, elements, 2)
	assert.Equal(t, "svc1", elements[0]["service"])
	assert.Equal(t, "v1.0.0", elements[0]["image"])
	assert.Equal(t, "charts/svc1", elements[0]["chart"])
	assert.Equal(t, "pr-123", elements[0]["namespace"])

	assert.Equal(t, "svc2", elements[1]["service"])
}

func TestGenerateApplicationSetLabels(t *testing.T) {
	gen := &ApplicationSetGenerator{}
	env := &v1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{Name: "pr-123"}}
	configs := map[string]ServiceConfig{"svc1": {Name: "svc1"}}

	appSet, err := gen.GenerateApplicationSet(context.Background(), env, []string{"svc1"}, configs)
	assert.NoError(t, err)

	labels := appSet.GetLabels()
	assert.Equal(t, "pr-123", labels["diverge.io/environment"])

	templateLabels, _, _ := unstructuredNestedMap(appSet.Object, "spec", "template", "metadata", "labels")
	assert.Equal(t, "pr-123", templateLabels["diverge.io/environment"])
}

func TestGenerateApplicationSetEmptyServices(t *testing.T) {
	gen := &ApplicationSetGenerator{}
	env := &v1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{Name: "pr-123"}}

	appSet, err := gen.GenerateApplicationSet(context.Background(), env, []string{}, map[string]ServiceConfig{})
	assert.NoError(t, err)

	spec, _, _ := unstructuredNestedMap(appSet.Object, "spec")
	generators, _, _ := unstructuredNestedSlice(spec, "generators")
	gen0 := generators[0].(map[string]interface{})
	list := gen0["list"].(map[string]interface{})
	elements := list["elements"].([]map[string]interface{})

	assert.Len(t, elements, 0)
}

// Helpers for extracting nested values safely
func unstructuredNestedMap(obj map[string]interface{}, fields ...string) (map[string]interface{}, bool, error) {
	var val interface{} = obj
	for _, field := range fields {
		if m, ok := val.(map[string]interface{}); ok {
			val = m[field]
		} else {
			return nil, false, nil
		}
	}
	m, ok := val.(map[string]interface{})
	return m, ok, nil
}

func unstructuredNestedSlice(obj map[string]interface{}, fields ...string) ([]interface{}, bool, error) {
	var val interface{} = obj
	for _, field := range fields {
		if m, ok := val.(map[string]interface{}); ok {
			val = m[field]
		} else {
			return nil, false, nil
		}
	}
	s, ok := val.([]interface{})
	return s, ok, nil
}
