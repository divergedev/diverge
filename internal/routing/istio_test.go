package routing

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func TestIstioRouter_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)

	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{
		{Group: "security.istio.io", Version: "v1"},
	})
	mapper.Add(schema.GroupVersionKind{Group: "security.istio.io", Version: "v1", Kind: "AuthorizationPolicy"}, meta.RESTScopeNamespace)

	c := fake.NewClientBuilder().WithScheme(scheme).WithRESTMapper(mapper).WithInterceptorFuncs(interceptor.Funcs{
		Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			if patch.Type() == types.ApplyPatchType {
				return cl.Create(ctx, obj)
			}
			return cl.Patch(ctx, obj, patch, opts...)
		},
	}).Build()
	router := &IstioRouter{Client: c}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "test-ns",
			UID:       "env-uid",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Routing: v1alpha1.EnvironmentRouting{
				DevIP: "100.64.1.2",
			},
			Deploy: v1alpha1.EnvironmentDeploy{
				Namespace: "same",
			},
		},
	}

	err := router.Reconcile(context.Background(), env)
	require.NoError(t, err)

	var policyList unstructured.UnstructuredList
	policyList.SetAPIVersion("security.istio.io/v1")
	policyList.SetKind("AuthorizationPolicyList")

	err = c.List(context.Background(), &policyList, client.InNamespace("test-ns"))
	require.NoError(t, err)
	require.Len(t, policyList.Items, 1)

	policy := policyList.Items[0]
	assert.Equal(t, "diverge-dev-test-env", policy.GetName())
	assert.Equal(t, "test-env", policy.GetLabels()["diverge.io/environment"])

	owners := policy.GetOwnerReferences()
	require.Len(t, owners, 1)
	assert.Equal(t, "Environment", owners[0].Kind)
	assert.Equal(t, "test-env", owners[0].Name)

	spec, ok, err := unstructured.NestedMap(policy.Object, "spec")
	require.NoError(t, err)
	require.True(t, ok)

	assert.Equal(t, "ALLOW", spec["action"])

	rules, _, _ := unstructured.NestedSlice(policy.Object, "spec", "rules")
	require.Len(t, rules, 2)

	// Check IP block rule
	rule1 := rules[0].(map[string]interface{})
	from1 := rule1["from"].([]interface{})[0].(map[string]interface{})
	source1 := from1["source"].(map[string]interface{})
	ipBlocks := source1["ipBlocks"].([]interface{})
	assert.Equal(t, "100.64.1.2/32", ipBlocks[0])

	// Check principals rule
	rule2 := rules[1].(map[string]interface{})
	from2 := rule2["from"].([]interface{})[0].(map[string]interface{})
	source2 := from2["source"].(map[string]interface{})
	principals := source2["principals"].([]interface{})
	assert.Equal(t, "cluster.local/ns/test-ns/sa/*", principals[0])

	// pure L4, no methods or operation
	_, ok, _ = unstructured.NestedSlice(rule1, "to")
	assert.False(t, ok, "should not have 'to' or 'operation' block for pure L4")
}

func TestIstioRouter_Teardown(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)

	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{
		{Group: "security.istio.io", Version: "v1"},
	})
	mapper.Add(schema.GroupVersionKind{Group: "security.istio.io", Version: "v1", Kind: "AuthorizationPolicy"}, meta.RESTScopeNamespace)

	c := fake.NewClientBuilder().WithScheme(scheme).WithRESTMapper(mapper).Build()
	router := &IstioRouter{Client: c}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "test-ns",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				Namespace: "same",
			},
		},
	}

	// create a fake policy to tear down
	policy := &unstructured.Unstructured{}
	policy.SetAPIVersion("security.istio.io/v1")
	policy.SetKind("AuthorizationPolicy")
	policy.SetName("diverge-dev-test-env")
	policy.SetNamespace("test-ns")
	policy.SetLabels(map[string]string{
		"diverge.io/environment": "test-env",
		"diverge.io/managed-by":  "diverge",
	})
	err := c.Create(context.Background(), policy)
	require.NoError(t, err)

	err = router.Teardown(context.Background(), env)
	assert.NoError(t, err)

	// verify deleted
	var policyList unstructured.UnstructuredList
	policyList.SetAPIVersion("security.istio.io/v1")
	policyList.SetKind("AuthorizationPolicyList")
	err = c.List(context.Background(), &policyList, client.InNamespace("test-ns"))
	require.NoError(t, err)
	assert.Len(t, policyList.Items, 0)
}

func TestIstioRouter_GetExternalURL(t *testing.T) {
	router := &IstioRouter{}
	env := &v1alpha1.Environment{}
	url := router.GetExternalURL(env)
	assert.Equal(t, "", url)
}
