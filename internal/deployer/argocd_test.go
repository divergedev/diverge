package deployer

import (
	"context"
	"errors"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/argocd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type fakeArgoApplicator struct {
	applied []*unstructured.Unstructured
	deleted bool
	status  []argocd.ApplicationStatus
	err     error
}

func (f *fakeArgoApplicator) ApplyApplication(_ context.Context, app *unstructured.Unstructured) error {
	if f.err != nil {
		return f.err
	}
	f.applied = append(f.applied, app)
	return nil
}

func (f *fakeArgoApplicator) ApplyApplications(_ context.Context, apps []*unstructured.Unstructured) error {
	if f.err != nil {
		return f.err
	}
	f.applied = apps
	return nil
}

func (f *fakeArgoApplicator) DeleteApplication(_ context.Context, _ string) error {
	if f.err != nil {
		return f.err
	}
	f.deleted = true
	return nil
}

func (f *fakeArgoApplicator) DeleteApplicationsForEnvironment(_ context.Context, _, _ string) error {
	if f.err != nil {
		return f.err
	}
	f.deleted = true
	return nil
}

func (f *fakeArgoApplicator) GetSyncStatus(_ context.Context, _, _ string) ([]argocd.ApplicationStatus, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.status, nil
}

func TestArgoDeployer_Lifecycle(t *testing.T) {
	appClient := &fakeArgoApplicator{
		status: []argocd.ApplicationStatus{
			{Name: "app-1", Service: "api", SyncStatus: "Synced", Health: "Healthy"},
		},
	}
	gen := &argocd.Generator{
		ArgoNamespace: "argocd",
		RepoURL:       "https://github.com/org/repo",
		Project:       "default",
	}
	cfgs := map[string]argocd.ServiceConfig{
		"api": {Name: "api", ChartPath: "charts/api"},
	}

	d := NewArgoDeployer(appClient, gen, cfgs)
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "pr-5", Namespace: "preview"},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{Branch: "feat/x"},
			Deploy: v1alpha1.EnvironmentDeploy{ChangedServices: []string{"api"}},
		},
	}

	require.NoError(t, d.Deploy(context.Background(), env))
	assert.Len(t, appClient.applied, 1)

	statuses, err := d.Status(context.Background(), env)
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	assert.Equal(t, "Healthy", statuses[0].Health)
	assert.Equal(t, "Synced", statuses[0].SyncStatus)

	require.NoError(t, d.Teardown(context.Background(), env))
	assert.True(t, appClient.deleted)
}

func TestArgoDeployer_ErrorPropagation(t *testing.T) {
	boom := errors.New("argo error")
	appClient := &fakeArgoApplicator{err: boom}
	gen := &argocd.Generator{ArgoNamespace: "argocd"}
	d := NewArgoDeployer(appClient, gen, nil)

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "pr-5", Namespace: "preview"},
	}
	_, err := d.Status(context.Background(), env)
	assert.ErrorIs(t, err, boom)
	assert.ErrorIs(t, d.Teardown(context.Background(), env), boom)
}
