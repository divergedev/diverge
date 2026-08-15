package crossns

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func buildFakeClient() client.Client {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = gatewayv1.Install(scheme)
	return fake.NewClientBuilder().WithScheme(scheme).Build()
}

func TestEnsureReferenceGrant_Creates(t *testing.T) {
	c := buildFakeClient()
	err := EnsureReferenceGrant(context.Background(), c, "from-ns", "to-ns")
	require.NoError(t, err)

	grant := &gatewayv1.ReferenceGrant{}
	err = c.Get(context.Background(), client.ObjectKey{Name: "diverge-crossns-from-ns", Namespace: "to-ns"}, grant)
	require.NoError(t, err)

	assert.Equal(t, "diverge-crossns-from-ns", grant.Name)
	assert.Equal(t, "to-ns", grant.Namespace)
	require.Len(t, grant.Spec.From, 1)
	assert.Equal(t, gatewayv1.Namespace("from-ns"), grant.Spec.From[0].Namespace)
}

func TestEnsureReferenceGrant_AlreadyExists(t *testing.T) {
	c := buildFakeClient()
	err := EnsureReferenceGrant(context.Background(), c, "from-ns", "to-ns")
	require.NoError(t, err)

	// Second time shouldn't return error
	err = EnsureReferenceGrant(context.Background(), c, "from-ns", "to-ns")
	require.NoError(t, err)
}

func TestEnsureReferenceGrant_UniquePerSourceNamespace(t *testing.T) {
	c := buildFakeClient()
	err := EnsureReferenceGrant(context.Background(), c, "from-ns-1", "to-ns")
	require.NoError(t, err)
	err = EnsureReferenceGrant(context.Background(), c, "from-ns-2", "to-ns")
	require.NoError(t, err)

	grant1 := &gatewayv1.ReferenceGrant{}
	err = c.Get(context.Background(), client.ObjectKey{Name: "diverge-crossns-from-ns-1", Namespace: "to-ns"}, grant1)
	require.NoError(t, err)

	grant2 := &gatewayv1.ReferenceGrant{}
	err = c.Get(context.Background(), client.ObjectKey{Name: "diverge-crossns-from-ns-2", Namespace: "to-ns"}, grant2)
	require.NoError(t, err)
}

func TestEnsureReferenceGrant_SameNamespace(t *testing.T) {
	c := buildFakeClient()
	err := EnsureReferenceGrant(context.Background(), c, "same-ns", "same-ns")
	require.NoError(t, err)

	grant := &gatewayv1.ReferenceGrant{}
	err = c.Get(context.Background(), client.ObjectKey{Name: "diverge-crossns-same-ns", Namespace: "same-ns"}, grant)
	assert.True(t, apierrors.IsNotFound(err))
}
