package deployer

import (
	"context"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/argocd"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var _ Deployer = (*NoopDeployer)(nil)
var _ Deployer = (*ArgoDeployer)(nil)

type fakeApplicator struct {
	appliedApps []*unstructured.Unstructured
	deletedEnvs []string
}

func (f *fakeApplicator) ApplyApplication(ctx context.Context, app *unstructured.Unstructured) error {
	f.appliedApps = append(f.appliedApps, app)
	return nil
}

func (f *fakeApplicator) ApplyApplications(ctx context.Context, apps []*unstructured.Unstructured) error {
	f.appliedApps = append(f.appliedApps, apps...)
	return nil
}

func (f *fakeApplicator) DeleteApplication(ctx context.Context, name string) error {
	return nil
}

func (f *fakeApplicator) DeleteApplicationsForEnvironment(ctx context.Context, envName string) error {
	f.deletedEnvs = append(f.deletedEnvs, envName)
	return nil
}

func (f *fakeApplicator) GetSyncStatus(ctx context.Context, envName string) ([]argocd.ApplicationStatus, error) {
	return nil, nil
}

func TestNoopDeployer(t *testing.T) {
	d := &NoopDeployer{}
	env := &v1alpha1.Environment{}

	err := d.Deploy(context.Background(), env)
	assert.NoError(t, err)

	err = d.Teardown(context.Background(), env)
	assert.NoError(t, err)
}

func TestArgoDeployerDeploy(t *testing.T) {
	fakeClient := &fakeApplicator{}
	generator := &argocd.Generator{
		ArgoNamespace: "argocd",
	}

	serviceConfigs := map[string]argocd.ServiceConfig{
		"svc1": {Name: "svc1", ChartPath: "charts/svc1"},
	}

	d := NewArgoDeployer(fakeClient, generator, serviceConfigs)

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "test-ns",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				ChangedServices: []string{"svc1"},
			},
		},
	}

	err := d.Deploy(context.Background(), env)
	assert.NoError(t, err)

	assert.Len(t, fakeClient.appliedApps, 1)
	assert.Equal(t, "diverge-test-ns-test-env-svc1", fakeClient.appliedApps[0].GetName())
}

func TestArgoDeployerTeardown(t *testing.T) {
	fakeClient := &fakeApplicator{}
	generator := &argocd.Generator{}
	d := NewArgoDeployer(fakeClient, generator, nil)

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-env",
		},
	}

	err := d.Teardown(context.Background(), env)
	assert.NoError(t, err)

	assert.Len(t, fakeClient.deletedEnvs, 1)
	assert.Equal(t, "test-env", fakeClient.deletedEnvs[0])
}
