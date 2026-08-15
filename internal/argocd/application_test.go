package argocd

import (
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestGenerate(t *testing.T) {
	gen := &Generator{
		ArgoNamespace:     "argocd",
		RepoURL:           "https://github.com/myorg/charts.git",
		DestinationServer: "https://kubernetes.default.svc",
		Project:           "default",
	}

	tests := []struct {
		name            string
		env             *v1alpha1.Environment
		changedServices []string
		configs         map[string]ServiceConfig
		wantErr         string
		expectedApps    int
		check           func(t *testing.T, env *v1alpha1.Environment, apps []*unstructured.Unstructured)
	}{
		{
			name: "single service - default mode (same)",
			env: &v1alpha1.Environment{
				ObjectMeta: metav1.ObjectMeta{Name: "preview-mr-42", Namespace: "test-ns", UID: "uid"},
				Spec: v1alpha1.EnvironmentSpec{
					Source: v1alpha1.EnvironmentSource{Branch: "feature-branch", MR: 42},
				},
			},
			changedServices: []string{"api"},
			configs: map[string]ServiceConfig{
				"api": {Name: "api", Tag: "abc123", ChartPath: "charts/api"},
			},
			expectedApps: 1,
			check: func(t *testing.T, env *v1alpha1.Environment, apps []*unstructured.Unstructured) {
				app := apps[0]
				assert.Equal(t, "argoproj.io/v1alpha1", app.GetAPIVersion())
				assert.Equal(t, "Application", app.GetKind())
				assert.Equal(t, "diverge-test-ns-preview-mr-42-api", app.GetName())
				assert.Equal(t, "argocd", app.GetNamespace())

				labels := app.GetLabels()
				assert.Equal(t, "preview-mr-42", labels["diverge.io/environment"])
				assert.Equal(t, "test-ns", labels["diverge.io/environment-namespace"])
				assert.Equal(t, "api", labels["diverge.io/service"])
				assert.Equal(t, "diverge", labels["diverge.io/managed-by"])

				annots := app.GetAnnotations()
				assert.Equal(t, "test-ns", annots["diverge.io/environment-namespace"])
				assert.Equal(t, "feature-branch", annots["diverge.io/source-branch"])
				assert.Equal(t, "42", annots["diverge.io/source-mr"])

				ownerRefs := app.GetOwnerReferences()
				assert.Empty(t, ownerRefs)

				finalizers := app.GetFinalizers()
				assert.Contains(t, finalizers, "resources-finalizer.argocd.argoproj.io")

				repoURL, _, _ := unstructured.NestedString(app.Object, "spec", "source", "repoURL")
				assert.Equal(t, "https://github.com/myorg/charts.git", repoURL)
				path, _, _ := unstructured.NestedString(app.Object, "spec", "source", "path")
				assert.Equal(t, "charts/api", path)
				targetRev, _, _ := unstructured.NestedString(app.Object, "spec", "source", "targetRevision")
				assert.Equal(t, "feature-branch", targetRev)

				params, _, _ := unstructured.NestedSlice(app.Object, "spec", "source", "helm", "parameters")
				require.Len(t, params, 1)
				param := params[0].(map[string]interface{})
				assert.Equal(t, "image.tag", param["name"])
				assert.Equal(t, "abc123", param["value"])

				ns, _, _ := unstructured.NestedString(app.Object, "spec", "destination", "namespace")
				assert.Equal(t, "test-ns", ns)
				srv, _, _ := unstructured.NestedString(app.Object, "spec", "destination", "server")
				assert.Equal(t, "https://kubernetes.default.svc", srv)

				syncOptions, _, _ := unstructured.NestedSlice(app.Object, "spec", "syncPolicy", "syncOptions")
				assert.Contains(t, syncOptions, "CreateNamespace=true")
				assert.Contains(t, syncOptions, "ServerSideApply=true")
				assert.Contains(t, syncOptions, "IgnoreExtraneous=true")
				assert.NotContains(t, syncOptions, "Prune=false")

				ignoreDiffs, _, _ := unstructured.NestedSlice(app.Object, "spec", "ignoreDifferences")
				require.Len(t, ignoreDiffs, 3)

				var kinds []string
				for _, d := range ignoreDiffs {
					dm := d.(map[string]interface{})
					kinds = append(kinds, dm["kind"].(string))
				}
				assert.Contains(t, kinds, "HTTPRoute")
				assert.Contains(t, kinds, "EndpointSlice")
				assert.Contains(t, kinds, "Deployment")
			},
		},
		{
			name: "single service - create mode",
			env: &v1alpha1.Environment{
				ObjectMeta: metav1.ObjectMeta{Name: "preview-mr-42", Namespace: "test-ns", UID: "uid"},
				Spec: v1alpha1.EnvironmentSpec{
					Deploy: v1alpha1.EnvironmentDeploy{Namespace: "create"},
					Source: v1alpha1.EnvironmentSource{Branch: "feature-branch", MR: 42},
				},
			},
			changedServices: []string{"api"},
			configs: map[string]ServiceConfig{
				"api": {Name: "api", Tag: "abc123", ChartPath: "charts/api"},
			},
			expectedApps: 1,
			check: func(t *testing.T, env *v1alpha1.Environment, apps []*unstructured.Unstructured) {
				app := apps[0]
				ns, _, _ := unstructured.NestedString(app.Object, "spec", "destination", "namespace")
				assert.Equal(t, env.PreviewNamespace(), ns)
			},
		},
		{
			name: "multiple services",
			env: &v1alpha1.Environment{
				ObjectMeta: metav1.ObjectMeta{Name: "preview-mr-42", Namespace: "test-ns", UID: "uid"},
			},
			changedServices: []string{"api", "web", "worker"},
			configs: map[string]ServiceConfig{
				"api":    {Name: "api", Tag: "v1.0.0", ChartPath: "charts/api"},
				"web":    {Name: "web", Tag: "v2.0.0", ChartPath: "charts/web"},
				"worker": {Name: "worker", Tag: "v3.0.0", ChartPath: "charts/worker"},
			},
			expectedApps: 3,
			check: func(t *testing.T, env *v1alpha1.Environment, apps []*unstructured.Unstructured) {
				names := make(map[string]bool)
				for _, app := range apps {
					name := app.GetName()
					names[name] = true
					labels := app.GetLabels()
					assert.Equal(t, "preview-mr-42", labels["diverge.io/environment"])
					assert.NotEmpty(t, labels["diverge.io/service"])
				}
				assert.True(t, names["diverge-test-ns-preview-mr-42-api"])
				assert.True(t, names["diverge-test-ns-preview-mr-42-web"])
				assert.True(t, names["diverge-test-ns-preview-mr-42-worker"])
			},
		},
		{
			name: "delta deployment",
			env: &v1alpha1.Environment{
				ObjectMeta: metav1.ObjectMeta{Name: "preview-mr-42", Namespace: "test-ns", UID: "uid"},
			},
			changedServices: []string{"api"},
			configs: map[string]ServiceConfig{
				"api":    {Name: "api", Tag: "v1.1.0", ChartPath: "charts/api"},
				"web":    {Name: "web", Tag: "v2.0.0", ChartPath: "charts/web"},
				"worker": {Name: "worker", Tag: "v3.0.0", ChartPath: "charts/worker"},
			},
			expectedApps: 1,
			check: func(t *testing.T, env *v1alpha1.Environment, apps []*unstructured.Unstructured) {
				assert.Equal(t, "diverge-test-ns-preview-mr-42-api", apps[0].GetName())
			},
		},
		{
			name: "empty services",
			env: &v1alpha1.Environment{
				ObjectMeta: metav1.ObjectMeta{Name: "preview-mr-42", Namespace: "test-ns", UID: "uid"},
			},
			changedServices: []string{},
			configs:         map[string]ServiceConfig{},
			expectedApps:    0,
		},
		{
			name: "missing config",
			env: &v1alpha1.Environment{
				ObjectMeta: metav1.ObjectMeta{Name: "preview-mr-42", Namespace: "test-ns", UID: "uid"},
			},
			changedServices: []string{"missing"},
			configs:         map[string]ServiceConfig{},
			wantErr:         "service config not found for \"missing\"",
		},
		{
			name: "denied namespace - create mode",
			env: &v1alpha1.Environment{
				ObjectMeta: metav1.ObjectMeta{Name: "kube-system", Namespace: "test-ns", UID: "uid"},
				Spec: v1alpha1.EnvironmentSpec{
					Deploy: v1alpha1.EnvironmentDeploy{Namespace: "create"},
				},
			},
			changedServices: []string{"api"},
			configs: map[string]ServiceConfig{
				"api": {Name: "api", Tag: "v1", ChartPath: "charts/api"},
			},
			expectedApps: 1,
			check: func(t *testing.T, env *v1alpha1.Environment, apps []*unstructured.Unstructured) {
				ns, _, _ := unstructured.NestedString(apps[0].Object, "spec", "destination", "namespace")
				assert.Equal(t, env.PreviewNamespace(), ns)
			},
		},
		{
			name: "denied namespace - same mode blocked",
			env: &v1alpha1.Environment{
				ObjectMeta: metav1.ObjectMeta{Name: "preview-123", Namespace: "default", UID: "uid"},
				Spec: v1alpha1.EnvironmentSpec{
					Deploy: v1alpha1.EnvironmentDeploy{Namespace: "same"},
				},
			},
			changedServices: []string{"api"},
			configs: map[string]ServiceConfig{
				"api": {Name: "api", Tag: "v1", ChartPath: "charts/api"},
			},
			wantErr: "destination namespace \"default\" is forbidden",
		},
		{
			name: "kustomize source type",
			env: &v1alpha1.Environment{
				ObjectMeta: metav1.ObjectMeta{Name: "preview-mr-42", Namespace: "product-clinical", UID: "uid"},
				Spec: v1alpha1.EnvironmentSpec{
					Source: v1alpha1.EnvironmentSource{Branch: "feat/patient-search"},
				},
			},
			changedServices: []string{"patient-api"},
			configs: map[string]ServiceConfig{
				"patient-api": {
					Name:       "patient-api",
					SourceType: "kustomize",
					Path:       "apps/dev/apps/patient-api",
					Image:      "registry.example.com/patient-api",
					Tag:        "mr-42-abc123",
				},
			},
			expectedApps: 1,
			check: func(t *testing.T, env *v1alpha1.Environment, apps []*unstructured.Unstructured) {
				app := apps[0]

				// Verify source uses kustomize, not helm
				source, _, _ := unstructured.NestedMap(app.Object, "spec", "source")
				assert.Equal(t, "apps/dev/apps/patient-api", source["path"])
				assert.Equal(t, "feat/patient-search", source["targetRevision"])

				// Verify kustomize image override
				images, _, _ := unstructured.NestedStringSlice(app.Object, "spec", "source", "kustomize", "images")
				assert.Len(t, images, 1)
				assert.Equal(t, "registry.example.com/patient-api:mr-42-abc123", images[0])

				// Verify no helm block
				_, helmFound, _ := unstructured.NestedMap(app.Object, "spec", "source", "helm")
				assert.False(t, helmFound, "kustomize source should not have helm block")
			},
		},
		{
			name: "kustomize source with tag only (no image)",
			env: &v1alpha1.Environment{
				ObjectMeta: metav1.ObjectMeta{Name: "preview-mr-50", Namespace: "test-ns", UID: "uid"},
			},
			changedServices: []string{"api"},
			configs: map[string]ServiceConfig{
				"api": {
					Name:       "api",
					SourceType: "kustomize",
					Path:       "apps/dev/apps/api",
					Tag:        "v1.2.3",
				},
			},
			expectedApps: 1,
			check: func(t *testing.T, env *v1alpha1.Environment, apps []*unstructured.Unstructured) {
				images, _, _ := unstructured.NestedStringSlice(apps[0].Object, "spec", "source", "kustomize", "images")
				assert.Len(t, images, 1)
				assert.Equal(t, "api:v1.2.3", images[0], "should use service name as default image name")
			},
		},
		{
			name: "helm source type explicit",
			env: &v1alpha1.Environment{
				ObjectMeta: metav1.ObjectMeta{Name: "preview-mr-42", Namespace: "test-ns", UID: "uid"},
			},
			changedServices: []string{"api"},
			configs: map[string]ServiceConfig{
				"api": {Name: "api", SourceType: "helm", Tag: "v1", ChartPath: "charts/api"},
			},
			expectedApps: 1,
			check: func(t *testing.T, env *v1alpha1.Environment, apps []*unstructured.Unstructured) {
				// Verify helm block exists
				params, _, _ := unstructured.NestedSlice(apps[0].Object, "spec", "source", "helm", "parameters")
				assert.Len(t, params, 1)

				// Verify no kustomize block
				_, kFound, _ := unstructured.NestedMap(apps[0].Object, "spec", "source", "kustomize")
				assert.False(t, kFound, "helm source should not have kustomize block")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apps, err := gen.Generate(tt.env, tt.changedServices, tt.configs)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
				assert.Len(t, apps, tt.expectedApps)
				if tt.check != nil {
					tt.check(t, tt.env, apps)
				}
			}
		})
	}
}
