package controller

import (
	"context"
	"testing"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func newTestPreviewGroupReconcilerWithNotifier(notifier *mockNotifier, objs ...client.Object) (*PreviewGroupReconciler, client.Client) {
	r, c := newTestPreviewGroupReconciler(objs...)
	r.Notifier = notifier
	return r, c
}

func TestPreviewGroupLifecycle_FullCycle(t *testing.T) {
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pg",
			Namespace: "default",
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "web", Image: "web:latest"},
				{Name: "api", Image: "api:latest"},
			},
		},
	}

	notifier := &mockNotifier{}
	r, c := newTestPreviewGroupReconcilerWithNotifier(notifier, pg)
	req := reconcile.Request{NamespacedName: types.NamespacedName{Name: "test-pg", Namespace: "default"}}

	// 1. Create -> Reconcile (adds finalizer)
	_, err := r.Reconcile(context.Background(), req)
	require.NoError(t, err)

	// 2. Reconcile again -> creates children
	_, err = r.Reconcile(context.Background(), req)
	require.NoError(t, err)

	// Verify 2 child environments created
	envList := &divergeiov1alpha1.EnvironmentList{}
	err = c.List(context.Background(), envList, client.InNamespace("default"))
	require.NoError(t, err)
	require.Len(t, envList.Items, 2)

	// 3. Update PG spec to remove 'api' service
	err = c.Get(context.Background(), req.NamespacedName, pg)
	require.NoError(t, err)
	pg.Spec.Services = []divergeiov1alpha1.PreviewGroupServiceSpec{
		{Name: "web", Image: "web:latest"},
	}
	err = c.Update(context.Background(), pg)
	require.NoError(t, err)

	// 4. Reconcile -> verify orphaned 'api' Environment is deleted
	_, err = r.Reconcile(context.Background(), req)
	require.NoError(t, err)

	err = c.List(context.Background(), envList, client.InNamespace("default"))
	require.NoError(t, err)
	require.Len(t, envList.Items, 1)
	assert.Equal(t, "web:latest", envList.Items[0].Spec.ServiceConfig.Image)

	// 5. Verify mock notifier received UpdateGroupStatus call
	assert.NotEmpty(t, notifier.statusCalls)

	// 6. Delete PreviewGroup (set DeletionTimestamp + finalizer)
	err = c.Get(context.Background(), req.NamespacedName, pg)
	require.NoError(t, err)
	err = c.Delete(context.Background(), pg)
	require.NoError(t, err)

	// 7. Reconcile -> verify remaining child is deleted
	_, err = r.Reconcile(context.Background(), req)
	require.NoError(t, err)

	err = c.List(context.Background(), envList, client.InNamespace("default"))
	require.NoError(t, err)
	require.Len(t, envList.Items, 0)

	// 8. Reconcile again -> verify finalizer removed, PG deleted
	_, err = r.Reconcile(context.Background(), req)
	require.NoError(t, err)

	err = c.Get(context.Background(), req.NamespacedName, pg)
	require.Error(t, err) // Should be not found

	// 9. Verify mock notifier received PostGroupTeardown call
	assert.NotEmpty(t, notifier.teardownCalls)
}
