package deployer

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func TestLocalDeployer_Deploy(t *testing.T) {
	c := fake.NewClientBuilder().WithInterceptorFuncs(withApplyMock()).Build()
	d := &LocalDeployer{Client: c}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				Endpoint: "100.100.100.100:8080",
			},
		},
	}

	err := d.Deploy(context.Background(), env)
	if err != nil {
		t.Fatalf("Deploy failed: %v", err)
	}

	// Verify Service
	svc := &corev1.Service{}
	err = c.Get(context.Background(), client.ObjectKey{Name: "test-svc", Namespace: "default"}, svc)
	if err != nil {
		t.Fatalf("Failed to get service: %v", err)
	}
	if svc.Spec.ClusterIP != corev1.ClusterIPNone {
		t.Errorf("Expected ClusterIP to be None, got %v", svc.Spec.ClusterIP)
	}

	// Verify EndpointSlice
	eps := &discoveryv1.EndpointSlice{}
	err = c.Get(context.Background(), client.ObjectKey{Name: "test-svc", Namespace: "default"}, eps)
	if err != nil {
		t.Fatalf("Failed to get endpointslice: %v", err)
	}
	if len(eps.Endpoints) != 1 {
		t.Fatalf("Expected 1 endpoint, got %v", len(eps.Endpoints))
	}
	if eps.Endpoints[0].Addresses[0] != "100.100.100.100" {
		t.Errorf("Expected address 100.100.100.100, got %v", eps.Endpoints[0].Addresses[0])
	}
}

func TestLocalDeployer_Teardown(t *testing.T) {
	c := fake.NewClientBuilder().WithInterceptorFuncs(withApplyMock()).Build()
	d := &LocalDeployer{Client: c}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				Endpoint: "100.100.100.100:8080",
			},
		},
	}

	_ = d.Deploy(context.Background(), env)

	err := d.Teardown(context.Background(), env)
	if err != nil {
		t.Fatalf("Teardown failed: %v", err)
	}

	// Verify deletion
	eps := &discoveryv1.EndpointSlice{}
	err = c.Get(context.Background(), client.ObjectKey{Name: "test-svc", Namespace: "default"}, eps)
	if !errors.IsNotFound(err) {
		t.Errorf("Expected EndpointSlice to be deleted, got err: %v", err)
	}
}

func TestLocalDeployer_Status(t *testing.T) {
	c := fake.NewClientBuilder().WithInterceptorFuncs(withApplyMock()).Build()
	d := &LocalDeployer{Client: c}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				Endpoint: "100.100.100.100:8080",
			},
		},
	}

	status, err := d.Status(context.Background(), env)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if status[0].Health != "Missing" {
		t.Errorf("Expected Missing, got %v", status[0].Health)
	}

	_ = d.Deploy(context.Background(), env)

	status, err = d.Status(context.Background(), env)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if status[0].Health != "Healthy" {
		t.Errorf("Expected Healthy, got %v", status[0].Health)
	}
}

func TestLocalDeployer_Deploy_Update(t *testing.T) {
	c := fake.NewClientBuilder().WithInterceptorFuncs(withApplyMock()).Build()
	d := &LocalDeployer{Client: c}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			ServiceConfig: &v1alpha1.ServicePreviewConfig{
				Endpoint: "100.100.100.100:8080",
			},
		},
	}

	_ = d.Deploy(context.Background(), env)

	env.Spec.ServiceConfig.Endpoint = "100.100.100.200:8081"
	err := d.Deploy(context.Background(), env)
	if err != nil {
		t.Fatalf("Deploy update failed: %v", err)
	}

	eps := &discoveryv1.EndpointSlice{}
	_ = c.Get(context.Background(), client.ObjectKey{Name: "test-svc", Namespace: "default"}, eps)
	if eps.Endpoints[0].Addresses[0] != "100.100.100.200" {
		t.Errorf("Expected updated address 100.100.100.200, got %v", eps.Endpoints[0].Addresses[0])
	}
	if *eps.Ports[0].Port != 8081 {
		t.Errorf("Expected updated port 8081, got %v", *eps.Ports[0].Port)
	}
}

func withApplyMock() interceptor.Funcs {
	return interceptor.Funcs{
		Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			if patch.Type() == "application/apply-patch+yaml" {
				clone := obj.DeepCopyObject().(client.Object)
				err := c.Get(ctx, client.ObjectKeyFromObject(obj), clone)
				if err != nil {
					if errors.IsNotFound(err) {
						return c.Create(ctx, obj)
					}
					return err
				}
				obj.SetResourceVersion(clone.GetResourceVersion())
				return c.Update(ctx, obj)
			}
			return c.Patch(ctx, obj, patch, opts...)
		},
	}
}
