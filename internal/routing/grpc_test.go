package routing

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func TestGRPCRouter_Reconcile(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	r := NewGRPCRouter(c, "default")

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-env",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				ChangedServices: []string{"svc1", "svc2"},
			},
			Routing: v1alpha1.EnvironmentRouting{
				HeaderKey:   "x-custom-env",
				HeaderValue: "custom-val",
			},
		},
	}

	err := r.Reconcile(context.Background(), env)
	assert.NoError(t, err)

	// Verify created resource
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("gateway.networking.k8s.io/v1")
	u.SetKind("GRPCRoute")
	err = c.Get(context.Background(), client.ObjectKey{Name: "test-env-svc1", Namespace: "default"}, u)
	assert.NoError(t, err)

	rules, _, _ := unstructured.NestedSlice(u.Object, "spec", "rules")
	assert.Len(t, rules, 1)

	matches, _, _ := unstructured.NestedSlice(rules[0].(map[string]interface{}), "matches")
	headers, _, _ := unstructured.NestedSlice(matches[0].(map[string]interface{}), "headers")
	assert.Equal(t, "x-custom-env", headers[0].(map[string]interface{})["name"])
	assert.Equal(t, "custom-val", headers[0].(map[string]interface{})["value"])
}

func TestGRPCRouter_Teardown(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	r := NewGRPCRouter(c, "default")

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-env",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{
				ChangedServices: []string{"svc1"},
			},
		},
	}

	// Pre-create the resource
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("gateway.networking.k8s.io/v1")
	u.SetKind("GRPCRoute")
	u.SetName("test-env-svc1")
	u.SetNamespace("default")
	_ = c.Create(context.Background(), u)

	err := r.Teardown(context.Background(), env)
	assert.NoError(t, err)

	// Verify deleted
	err = c.Get(context.Background(), client.ObjectKey{Name: "test-env-svc1", Namespace: "default"}, u)
	assert.Error(t, err)
}
